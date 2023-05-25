#!/bin/bash

# Define the list of microservices in the monorepo
MICROSERVICES=("api-gateway" "events-manager" "blob-service" "audit-service" "ci-service" "cd-service" "cloud-mgmt")

# Loop through each microservice and generate the Kubernetes YAML files
for MICROSERVICE in "${MICROSERVICES[@]}"
do
  # Generate the Kubernetes deployment YAML file for the microservice
  cd $MICROSERVICE
  docker build -t $MICROSERVICE .
  docker push $MICROSERVICE
  cat deployment.yaml.template | sed "s/{{MICROSERVICE}}/$MICROSERVICE/g" > deployment.yaml

  # Generate the Kubernetes service YAML file for the microservice
  cat service.yaml.template | sed "s/{{MICROSERVICE}}/$MICROSERVICE/g" > service.yaml

  # Deploy the microservice to Kubernetes
  kubectl apply -f deployment.yaml
  kubectl apply -f service.yaml
done
