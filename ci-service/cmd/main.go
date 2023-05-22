package main

import "github.com/miladhzzzz/milx-cloud-init/ci-service/internal/eventctl"

// TODO after build is done we should add the processed data in a different topic
// TODO move to internal/event-processor
// TODO push image
// TODO log build output and then add to "processed" topic
// TODO refactor the nonsense names of pkgs

func main() {
	eventctl.KafkaEventProcessor()
}
