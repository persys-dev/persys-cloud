#!/bin/bash

manifest="/manifests"
# Check if Docker is installed and running
if ! command -v docker &> /dev/null
then
    echo "Docker is not installed. Please install Docker and try again."
    exit
fi

if ! docker info &> /dev/null
then
    echo "Docker is not running. Please start Docker and try again."
    exit
fi

# Check if kubectl is installed and connected to a cluster
if ! command -v kubectl &> /dev/null
then
    echo "kubectl is not installed. Please install kubectl and try again."
    exit
fi

if ! kubectl cluster-info &> /dev/null
then
    echo "kubectl is not connected to a cluster. Please connect to a cluster and try again."
    exit
fi

# Find Dockerfiles and build images
for dir in */ ; do
    if [ -f "$dir/Dockerfile" ]; then
        image_name=$(basename "$dir")
        if docker build -t "$image_name" "$dir"; then
          docker push "$image_name"
            echo "Successfully built $image_name image." >> reports.txt
        else
            echo "Failed to build $image_name image." >> reports.txt
            continue
        fi
        # Generate Kubernetes deployment manifest
        # shellcheck disable=SC2094
        cat cat $manifest + "/" +  deployment.yaml.template | sed "s/{{MICROSERVICE}}/$MICROSERVICE/g" sed"s/{{IMAGE}}/$image_name/g" > $manifest + "/" + "$image_name-deployment.yaml"
        # shellcheck disable=SC2094
        cat cat $manifest + "/" +  service.yaml.template | sed "s/{{MICROSERVICE}}/$MICROSERVICE/g" > $manifest + "/" + "$image_name-service.yaml"
        # Apply Kubernetes deployment
        if kubectl apply -f "$manifest"; then
            echo "Successfully deployed $image_name." >> reports.txt
        else
            echo "Failed to deploy $image_name." >> reports.txt
        fi
    fi
done