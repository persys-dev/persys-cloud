package githubManagerClient

import (
	gm "github.com/miladhzzzz/milx-cloud-init/api-gateway/pkg/grpc-clients/events-manager/pb"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"log"
)

func InitGmClient() gm.GithubServiceClient {
	cc, err := grpc.Dial("events-manager.persys.svc.cluster.local:8662", grpc.WithTransportCredentials(insecure.NewCredentials()))

	if err != nil {
		log.Fatalf("we are fucked: %v", err)
	}

	return gm.NewGithubServiceClient(cc)
}
