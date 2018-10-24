package locking

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/Azure/azure-storage-blob-go/2016-05-31/azblob"
	"github.com/cenkalti/backoff"
	"github.com/satori/go.uuid"
)

const (
	lockBlobNamePrefix = "azlk-"           // This is appended to the blob containers created by the library
	lockContainerName  = "azlockcontainer" // This is the name of the container used by the blobs created for locking

)

type (
	// Lock represents the status of a lock
	Lock struct {
		ctx           context.Context
		used          bool                        // Set to True when a lock has been unlocked
		lockAcquired  bool                        // Set to True when a lock has been acquired
		panic         func(string)                // Used for testing to allow panic call to be mocked
		unlockContext func(context.Context) error // Used by 'UnlockWhenCancelled' behavior to pass temporary context to unlock
		cancel        context.CancelFunc          // Cancel is used internally to exit goRoutines of behaviors
		blobURL       azblob.BlobURL              // URL of the blob used for this lock
		internalMutex sync.Mutex                  // This is used to prevent multi threaded issues when updating 'used' and 'lockAcquired'

		// LockTTL is the duration for which the lock is to be held
		// Valid options: 15sec -> 60sec due to Azure Blob https://docs.microsoft.com/en-us/rest/api/storageservices/lease-container
		LockTTL time.Duration

		// LockLost This channel is signaled by the 'AutoRenew' behavior if the lock is lost
		LockLost chan struct{}

		// LockID is the ID of the underlying blob lease
		LockID uuid.UUID

		// Lock will acquire a lock for the specified name
		Lock func() error

		// Renew will renew the lock, if present
		// or return an error if no lock is held
		Renew func() error

		// Unlock will release the lock, if present
		// or return an error if no lock is held
		Unlock func() error
	}

	// BehaviorFunc is a type converter that allows a func to be used as a `Behavior`
	BehaviorFunc func(*Lock) *Lock
)

var (
	// defaultLockBehaviors are the behaviors which are used when no behavior parameters are provided
	defaultLockBehaviors = []BehaviorFunc{AutoRenewLock, PanicOnLostLock, UnlockWhenContextCancelled, RetryObtainingLock}

	// azBlobRetryOptions are the default retry settings used for the azure storage calls
	azBlobRetryOptions = azblob.RetryOptions{
		Policy:   azblob.RetryPolicyExponential,
		MaxTries: 3,
	}

	// AutoRenewLock configures the lock to autorenew itself
	AutoRenewLock = BehaviorFunc(func(l *Lock) *Lock {
		go func() {
			for {
				select {
				case <-l.ctx.Done():
					// Context has been cancelled, exit so can be gc'd
					return
				case <-time.Tick(l.LockTTL / 2):
					// If the 'lock' function hasn't been used yet spin
					if !l.lockAcquired {
						continue
					}
					// Do a renew. If we fail, clean up and notify that the lock is lost
					err := l.Renew()
					if err != nil {
						l.cancel()
						l.LockLost <- struct{}{}
						return
					}
				case <-l.LockLost:
					return
				}
			}
		}()
		return l
	})

	// RetryObtainingLock configures the lock to retry getting a lock if it is already held
	RetryObtainingLock = BehaviorFunc(func(l *Lock) *Lock {
		// Assuming locks will be initialised with roughly
		// the correct TTL required to perform the operation
		// lets give it 10x time to acquire it
		obtainLockBackoffPolicy := backoff.NewExponentialBackOff()
		obtainLockBackoffPolicy.MaxElapsedTime = l.LockTTL * 10
		existingLockFunc := l.Lock

		// Replace existing lock function with exponential retrying one
		l.Lock = func() error { return backoff.Retry(existingLockFunc, obtainLockBackoffPolicy) }

		return l
	})

	// UnlockWhenContextCancelled will remove a lease when a context is cancelled
	UnlockWhenContextCancelled = BehaviorFunc(func(l *Lock) *Lock {
		go func() {
			for {
				select {
				case <-l.ctx.Done():
					// If the 'lock' function wasn't ever called don't worry
					if !l.lockAcquired {
						return
					}
					// The original context is dead but we don't want to leave the lock in place
					// so lets create a new context and give it 3 seconds to get the job done
					ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(time.Second*5))
					defer cancel()
					l.unlockContext(ctx) //nolint: errcheck

					return
				case <-l.LockLost:
					return
				}
			}
		}()
		return l
	})

	// PanicOnLostLock configures the lock to autorenew itself
	PanicOnLostLock = BehaviorFunc(func(l *Lock) *Lock {
		go func() {
			select {
			case <-l.ctx.Done():
				return
			case <-l.LockLost:
				l.Unlock() //nolint: errcheck
				l.panic("Lock lost and 'PanicOnLostLock' set")
			}
		}()
		return l
	})
)

// NewLockInstance returns a new instance of a lock
//
// Params
// StorageAccountURL: HTTPS endpoint for your storage account eg. `https://mystorageaccount.blob.core.windows.net` if your account is named `mystorageaccount`
// StorageAccountKey: The access key for your storage account
// LockName: An alphanumberic string < 58 chars that will represent your lock.
// LockTTL: A duration between 15 and 60 seconds for which the lock will be held. Note, by default the `AutoRenew` behavior will renew locks until `Unlock` is called
//
// Advanced
// Behaviors: Funcs which allow you to mutate the lockInstance's behavior. Leave empty for default behavior
//
func NewLockInstance(ctxParent context.Context, storageAccountURL, storageAccountKey, lockName string, lockTTL time.Duration, behavior ...BehaviorFunc) (*Lock, error) {
	if storageAccountKey == "" {
		return nil, fmt.Errorf("Empty accountKey is invalid")
	}
	if lockTTL.Seconds() < 15 || lockTTL.Seconds() > 60 {
		return nil, fmt.Errorf("LockTTL of %v seconds is outside allowed range of 15-60seconds", lockTTL.Seconds())
	}
	if valid, err := IsValidLockName(lockName); !valid {
		return nil, err
	}
	storageAccountURLParsed, err := url.Parse(storageAccountURL)
	if err != nil {
		return nil, fmt.Errorf("failed to parse storageAccountUrl, err: %+v", err)
	}
	if storageAccountURLParsed.Scheme != "https" {
		return nil, fmt.Errorf("storageAccountURL should be 'https' Expect: 'https://mystorageaccount.blob.core.windows.net' Got: %s", storageAccountURL)
	}
	if storageAccountURLParsed.Path != "" {
		return nil, fmt.Errorf("storageAccountURL should be to the root of the storage account Expect: 'https://mystorageaccount.blob.core.windows.net' Got: %s", storageAccountURL)
	}
	if _, err = base64.StdEncoding.DecodeString(storageAccountKey); err != nil {
		return nil, fmt.Errorf("accountKey isn't valid base64 value - must be valid base64")
	}
	// Extract the accountname from the storage URL
	// for example 'https://mystorageaccount.blob.core.windows.net' -> 'mystorageaccount'
	accountName, err := extractAccountNameFromURL(storageAccountURLParsed)
	if err != nil {
		return nil, err
	}

	creds := azblob.NewSharedKeyCredential(accountName, storageAccountKey)

	// Create a ContainerURL object to a container
	u, _ := url.Parse(fmt.Sprintf("%s/%s", storageAccountURL, lockContainerName))
	containerURL := azblob.NewContainerURL(*u, azblob.NewPipeline(creds, azblob.PipelineOptions{Retry: azBlobRetryOptions}))

	_, err = containerURL.Create(ctxParent, nil, azblob.PublicAccessNone)
	// Create will return a ServiceCode of "ContainerAlreadyExists" if the container already exists
	// we only error on other conditions as it's expected that a container of this
	// name may already exist
	errResponse, isReponseError := err.(azblob.StorageError)
	if err != nil {
		if !isReponseError {
			return nil, err
		} else if errResponse.ServiceCode() != azblob.ServiceCodeContainerAlreadyExists {
			return nil, err
		}
	}

	// Create a blob, we use leases on the blob to implement the lock
	blobURL := containerURL.NewBlobURL(lockBlobNamePrefix + lockName)

	// Upload an empty blob
	buf := bytes.NewReader([]byte{})
	_, err = blobURL.ToBlockBlobURL().PutBlob(ctxParent, buf, azblob.BlobHTTPHeaders{}, azblob.Metadata{}, azblob.BlobAccessConditions{})

	// It's expected that a lock of this name may already exist
	// and may already have an active lease BUT for any other
	// ServiceCodes or errors we should return an error
	errResponse, isReponseError = err.(azblob.StorageError)
	if err != nil {
		if !isReponseError {
			return nil, err
		} else if isReponseError &&
			errResponse.ServiceCode() != azblob.ServiceCodeBlobAlreadyExists &&
			errResponse.ServiceCode() != azblob.ServiceCodeLeaseIDMissing {
			return nil, err
		}
	}

	// Create our own context which will be cancelled independently of
	// the parent context
	ctx, cancel := context.WithCancel(ctxParent)

	lockInstance := &Lock{
		ctx:      ctx,
		cancel:   cancel,
		blobURL:  blobURL,
		panic:    func(s string) { panic(s) },
		LockTTL:  lockTTL,
		LockLost: make(chan struct{}, 1),
		LockID:   uuid.NewV4(),
	}

	// This function handles unlocking
	// it accepts a context to allow locks to be unlocked
	// even after a context has been cancelled
	lockInstance.unlockContext = func(ctx context.Context) error {
		lockInstance.internalMutex.Lock()
		defer lockInstance.internalMutex.Unlock()

		if !lockInstance.lockAcquired {
			return fmt.Errorf("Lock not acquired, can't unlock")
		}
		if lockInstance.used {
			return fmt.Errorf("Lock instance already unlocked, cannot call unlock")
		}

		// No matter what happened cancel the context to close off the go routines running in behaviors
		defer lockInstance.cancel()

		_, err := lockInstance.blobURL.ReleaseLease(ctx, lockInstance.LockID.String(), azblob.HTTPAccessConditions{})

		if err != nil {
			return err
		}

		// Mark this lock instance as used to prevent reuse
		// as the library doesn't handle multiple uses per lock instance
		lockInstance.used = true

		return nil
	}

	lockInstance.Unlock = func() error {
		return lockInstance.unlockContext(lockInstance.ctx)
	}

	lockInstance.Lock = func() error {
		lockInstance.internalMutex.Lock()
		defer lockInstance.internalMutex.Unlock()

		if lockInstance.used {
			return fmt.Errorf("Lock instance already unlocked, cannot be reused")
		}
		if lockInstance.lockAcquired {
			return fmt.Errorf("Lock already acquire, call 'renew' to extend a lock")
		}

		_, err = lockInstance.blobURL.AcquireLease(lockInstance.ctx, lockInstance.LockID.String(), int32(lockTTL.Seconds()), azblob.HTTPAccessConditions{})
		if err != nil {
			return err
		}

		lockInstance.lockAcquired = true

		return nil
	}

	lockInstance.Renew = func() error {
		lockInstance.internalMutex.Lock()
		defer lockInstance.internalMutex.Unlock()

		if !lockInstance.lockAcquired {
			return fmt.Errorf("Lock not acquired, can't renew")
		}
		if lockInstance.used {
			return fmt.Errorf("Lock instance already used, cannot be reused")
		}
		_, err := lockInstance.blobURL.RenewLease(lockInstance.ctx, lockInstance.LockID.String(), azblob.HTTPAccessConditions{})
		if err != nil {
			return err
		}
		return nil
	}

	// If behaviors haven't been defined use the defaults
	if len(behavior) == 0 {
		behavior = defaultLockBehaviors
	}

	// Configure behaviors
	for _, b := range behavior {
		lockInstance = b(lockInstance)
	}

	return lockInstance, nil
}

func extractAccountNameFromURL(u *url.URL) (string, error) {
	parts := strings.Split(u.Hostname(), ".")
	if len(parts) < 1 {
		return "", fmt.Errorf("couldn't extract accountname from: %s", u.String())
	}
	return parts[0], nil
}

// validLockNameRegex is a regex used to check the chars are valid as an Azure Storage container name
var validLockNameRegex = regexp.MustCompile("^[a-z0-9]+(-[a-z0-9]+)*$")

// IsValidLockName checks if the lock name is between 3-58 (63 minus 5char prefix used) characters long
// and matches this regex @"^[a-z0-9]+(-[a-z0-9]+)*$"
func IsValidLockName(lockName string) (bool, error) {
	lockName = strings.ToLower(lockName)
	if len(lockName) < 3 || len(lockName) > 58 {
		return false, fmt.Errorf("lock name: %s must be between 3 and 58 characters long", lockName)
	}

	if !validLockNameRegex.MatchString(lockName) {
		return false, fmt.Errorf("lock name: %s must be alphanumberic with no characters other than '-' (regex '^[a-z0-9]+(-[a-z0-9]+)*$')", lockName)
	}

	return true, nil
}
