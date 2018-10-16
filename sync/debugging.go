package sync

import (
	"net/http"
	"net/http/httputil"

	"github.com/Azure/go-autorest/autorest"
	log "github.com/sirupsen/logrus"
)

func logRequest() autorest.PrepareDecorator {
	return func(p autorest.Preparer) autorest.Preparer {
		return autorest.PreparerFunc(func(r *http.Request) (*http.Request, error) {
			r, err := p.Prepare(r)
			if err != nil {
				log.Println(err)
			}
			dump, _ := httputil.DumpRequestOut(r, true)
			log.WithField("Request", string(dump)).Debug("Request to AzureFD API")
			return r, err
		})
	}
}

func logResponse() autorest.RespondDecorator {
	return func(p autorest.Responder) autorest.Responder {
		return autorest.ResponderFunc(func(r *http.Response) error {
			err := p.Respond(r)
			if err != nil {
				log.Println(err)
			}
			dump, _ := httputil.DumpResponse(r, true)
			log.WithField("Response", string(dump)).Debug("Response to AzureFD API")
			return err
		})
	}
}
