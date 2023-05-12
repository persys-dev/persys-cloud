package main

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/ThreeDotsLabs/watermill/message"
	"github.com/ThreeDotsLabs/watermill/message/router/middleware"
	"github.com/ThreeDotsLabs/watermill/message/router/plugin"
	"github.com/miladhzzzz/milx-cloud-init/ci-service/pkg/docker"
	"github.com/miladhzzzz/milx-cloud-init/ci-service/pkg/manifest"
	read_toml "github.com/miladhzzzz/milx-cloud-init/ci-service/pkg/read-toml"
	"github.com/miladhzzzz/milx-cloud-init/ci-service/pkg/watermill"
	"path/filepath"
)

type events struct {
	RepoID      int64  `json:"repoID"`
	UserID      int64  `json:"userID"`
	EventID     int64  `json:"eventID"`
	GitURL      string `json:"gitURL"`
	Commit      string `json:"commit"`
	AccessToken string `json:"accessTokne"`
	Private     bool   `json:"private"`
	State       string `json:"state"`
	CreatedAt   string `json:"createdAt"`
	Directory   string `json:"directory"`
}

var (
	reader       = ""
	consumeTopic = "events"
)

func jobToDb() {
	fmt.Println(reader)
}

// KafkaEventProcessor TODO after build is done we should add the processed data in a different topic
// TODO move to internal/event-processor
// TODO push image
// TODO log build output and then add to "processed" topic
// TODO refactor the nonsense names of pkgs
func KafkaEventProcessor() {

	consumedPayload := events{}

	subscriber := watermill.CreateSubscriber("ci-service")

	router, err := message.NewRouter(message.RouterConfig{}, watermill.Logger)
	if err != nil {
		panic(err)
	}

	router.AddPlugin(plugin.SignalsHandler)
	router.AddMiddleware(middleware.Recoverer)

	router.AddNoPublisherHandler(
		"ci-service",
		consumeTopic,
		subscriber,
		func(msg *message.Message) error {
			erp := json.Unmarshal(msg.Payload, &consumedPayload)
			if erp != nil {
				// When a handler returns an error, the default behavior is to send a Nack (negative-acknowledgement).
				// The message will be processed again.
				//
				// You can change the default behaviour by using middlewares, like Retry or PoisonQueue.
				// You can also implement your own middleware.
				return err
			}
			fmt.Printf("consumed message: %v", consumedPayload)

			//commit, directory, err := git.Gits(consumedPayload.GitURL, consumedPayload.Private, consumedPayload.AccessToken)

			//manifests, err := manifest.ScanToml(consumedPayload.Directory)

			data, ers := read_toml.ReadManifest(consumedPayload.Directory)

			if ers != nil {

			}
			fmt.Println(data)
			//if err != nil {
			//	return err
			//}

			//if manifests != nil {
			//	for _, m := range manifests {
			//		data, err2 := read_toml.ReadManifest(m)
			//		if err2 != nil {
			//			return err2
			//		}
			//		fmt.Println(data)
			//	}
			//}

			files, err := manifest.ScanDocker(consumedPayload.Directory)

			if err != nil {
				return err
			}

			//fmt.Println(files[0])

			//fmt.Printf("image build for commit: %v Dockerfile : %v", commit.Hash, files[0])
			//path := "C:\\" + filepath.Dir(files[0])

			//err = docker.ImageBuild(path, "ci:test")
			for _, file := range files {
				// IMPORTANT THIS ONLY WORKS FOR WINDOWS ENVIRONMENTS !!!! THIS IS FOR DEVELOPMENT ONLY !!!
				path := "C:\\" + filepath.Dir(file)
				//fmt.Println(path)
				err = docker.ImageBuild(path, "persys:latest")
				if err != nil {
					fmt.Println(err)
				}
			}
			return nil
		},
	)

	if err := router.Run(context.Background()); err != nil {
		panic(err)
	}

}

func main() {
	KafkaEventProcessor()
}
