# Azure Frontdoor Ingress

[![Build Status](https://travis-ci.com/lawrencegripper/azurefrontdooringress.svg?branch=master)](https://travis-ci.com/lawrencegripper/azurefrontdooringress)

## Status: WIP - Not working

The current code is blocked on [this issue we're seeing with FD API](https://github.com/Azure/azure-rest-api-specs/issues/4221). 

## What does this do?

The controller is intended to run as a secondary ingress in a cluster, for example alongside `traefik`. 

It looks for any `ingress` objects with the annotation `azure/frontdoor: enabled` and when found updates an Azure Front Door instance with the path based routing defined in the `ingress`. 

The aim is to allow a collection of clusters to sit behind Azure Front Door and have new services, and their routing rules, automatically added into Front Door as they are deployed to any of the clusters. 

## Testing

Add a .env file with the following defined for Azure connection 

```txt

AZURE_SUBSCRIPTION_ID=
AZURE_TENANT_ID=
AZURE_CLIENT_ID=
AZURE_CLIENT_SECRET=

```