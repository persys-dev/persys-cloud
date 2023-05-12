package docker

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/docker/docker/pkg/archive"
	"io"
	"time"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/client"
)

type ErrorLine struct {
	Error       string      `json:"error"`
	ErrorDetail ErrorDetail `json:"errorDetail"`
}

type ErrorDetail struct {
	Message string `json:"message"`
}

var dockerRegistryUserID = "192.168.13.1:5000/"

func ImageBuild(srcPath string, tag string) error {

	// initiate docker client
	dockerClient, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		fmt.Println(err.Error())
		return err
	}
	// EXTENDED TIMEOUT FOR SHITTY INTERNET * CHANGE THIS!
	ctx, cancel := context.WithTimeout(context.Background(), time.Second*1024)
	defer cancel()
	// make a tar from srcPath
	tar, err := archive.TarWithOptions(srcPath, &archive.TarOptions{})
	if err != nil {
		fmt.Println("tar bug!!!!!!!")
		return err
	}

	opts := types.ImageBuildOptions{
		Dockerfile: "Dockerfile",
		Tags:       []string{dockerRegistryUserID + tag},
		Remove:     true,
	}
	res, err := dockerClient.ImageBuild(ctx, tar, opts)
	if err != nil {
		fmt.Printf("image build bug: %v", err)
		return err
	}

	defer func(Body io.ReadCloser) {
		err := Body.Close()
		if err != nil {

		}
	}(res.Body)

	err = prints(res.Body)
	if err != nil {
		return err
	}

	return nil
}

// ImagePush TODO push image
func ImagePush() error {

	// initiate docker client
	dockerClient, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		fmt.Println(err.Error())
		return err
	}

	var authConfig = types.AuthConfig{
		Username:      "Your Docker Hub Username",
		Password:      "Your Docker Hub Password or Access Token",
		ServerAddress: "https://index.docker.io/v1/",
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second*120)
	defer cancel()

	authConfigBytes, _ := json.Marshal(authConfig)
	authConfigEncoded := base64.URLEncoding.EncodeToString(authConfigBytes)

	tag := dockerRegistryUserID + "/node-hello"
	opts := types.ImagePushOptions{RegistryAuth: authConfigEncoded}
	rd, err := dockerClient.ImagePush(ctx, tag, opts)
	if err != nil {
		return err
	}

	defer func(rd io.ReadCloser) {
		err := rd.Close()
		if err != nil {

		}
	}(rd)

	err = prints(rd)
	if err != nil {
		return err
	}

	return nil
}

// prints is a printing results from docker helper function
func prints(rd io.Reader) error {
	var lastLine string

	scanner := bufio.NewScanner(rd)
	for scanner.Scan() {
		lastLine = scanner.Text()
		fmt.Println(scanner.Text())
	}

	errLine := &ErrorLine{}
	err := json.Unmarshal([]byte(lastLine), errLine)
	if err != nil {
		return err
	}
	if errLine.Error != "" {
		return errors.New(errLine.Error)
	}

	if err := scanner.Err(); err != nil {
		return err
	}

	return nil
}
