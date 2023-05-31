package githubManagerClient

import (
	em "github.com/persys-dev/persys-cloud/api-gateway/pkg/grpc-clients/events-manager/pb"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"log"
)

func InitEventClient() em.EventServiceClient {
	cc, err := grpc.Dial("localhost:8662", grpc.WithTransportCredentials(insecure.NewCredentials()))

	if err != nil {
		log.Fatalf("we are fucked: %v", err)
	}

	return em.NewEventServiceClient(cc)
}
