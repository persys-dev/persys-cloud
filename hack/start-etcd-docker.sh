#!/bin/bash
if ! command -v docker &> /dev/null
then
    echo "Docker is not installed. Please install Docker and try again."
    exit 1
fi
 if [ $# -eq 0 ]
then
    # Start etcd in standalone mode
    echo "Starting etcd in standalone mode..."
    docker run -d \
      --name etcd \
      -p 2379:2379 \
      -p 4001:4001 \
      quay.io/coreos/etcd:v3.5.0 \
      etcd \
      --advertise-client-urls http://localhost:2379,http://localhost:4001 \
      --listen-client-urls http://0.0.0.0:2379,http://0.0.0.0:4001
    echo "etcd started in standalone mode and ports 2379 and 4001 are exposed on the local machine."
else
    # Start etcd in cluster mode
    if [[ ! $1 =~ ^[0-9]+$ ]]
    then
        echo "Invalid argument. Please specify a valid number of nodes."
        exit 1
    fi
    if [ "$1" -lt 1 ]
    then
        echo "Invalid argument. Please specify a number greater than or equal to 1."
        exit 1
    fi
    nodes=""
    echo "Starting $1-node etcd cluster..."
    for i in $(seq 1 "$1")
    do
        name="node$i"
        peer_port=$((2380 + $i - 1))
        client_port=$((2379 + $i - 1))
        node=",$name=http://localhost:$peer_port"
        nodes="$nodes$node"
        docker run -d \
          --name etcd-"$name" \
          -p $client_port:$client_port \
          -p $peer_port:$peer_port \
          quay.io/coreos/etcd:v3.5.0 \
          etcd \
          --name "$name" \
          --advertise-client-urls http://localhost:$client_port \
          --listen-client-urls http://0.0.0.0:$client_port \
          --initial-advertise-peer-urls http://localhost:$peer_port \
          --listen-peer-urls http://0.0.0.0:$peer_port \
          --initial-cluster node1=http://localhost:2380"$nodes" \
          --initial-cluster-token my-etcd-token \
          --initial-cluster-state new
    done
    echo "$1-node etcd cluster started and etcd ports are exposed on the local machine."
fi