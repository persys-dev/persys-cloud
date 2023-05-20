package main

import (
	"context"
	"fmt"
	proto "github.com/miladhzzzz/milx-cloud-init/events-manager/proto"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"log"
)

func main() {

	cc, err := grpc.Dial("localhost:8662", grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		log.Fatalf("we are fucked: %v", err)
	}

	defer func(cc *grpc.ClientConn) {
		err := cc.Close()
		if err != nil {

		}
	}(cc)

	c := proto.NewEventServiceClient(cc)
	res, err := c.PublishEvent(context.TODO(), &proto.EventMessage{})
	if err != nil {
		panic(err)
	}

	fmt.Printf("server response %v", res)
}
