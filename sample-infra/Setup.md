# Implemention and Setup for Azure Front Door Enhancements for AKS.

1. Create a Kubernetes Cluster or use an existing one. (https://docs.microsoft.com/en-us/azure/aks/tutorial-kubernetes-deploy-cluster?view=azure-cli-latest)

1. Install Helm locally with Tiller on your AKS cluster if you don't already have it installed.(https://docs.microsoft.com/en-us/azure/aks/kubernetes-helm)

1. [Use Helm to install the NGINX Ingress Controller](https://docs.microsoft.com/en-us/azure/aks/ingress-basic) In this case, we are using the [Kubernetes implemnetation of the NGINX Ingress Controller](https://github.com/kubernetes/ingress-nginx), not the NGINX or NGINX Plus controller.  You can learn more about the differences [here].(https://github.com/nginxinc/kubernetes-ingress/blob/master/docs/nginx-ingress-controllers.md)

1. Set up Azure Front Door.  Azure Front Door can be setup in the Portal or via [ARM Templates.](https://docs.microsoft.com/en-us/azure/frontdoor/front-door-quickstart-template-samples)

az group deployment create --name ExampleDeployment --resource-group ExampleGroup --template-file frontdoor.json --parameters @frontdoor.parameters.json

1. Install the Kubernetes Front Door Controller

