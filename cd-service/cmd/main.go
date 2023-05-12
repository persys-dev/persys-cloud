package main

import (
	"context"
	vx "k8s.io/api/core/v1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
	"net/http"

	"github.com/gin-gonic/gin"
)

func kubectl(KubeConfigPath string) (*kubernetes.Clientset, error) {

	kubeConfigPath := "../config/kube/k3s.yaml"

	kubeConfig, err := clientcmd.BuildConfigFromFlags("", kubeConfigPath)

	if err != nil {
		return nil, err
	}

	clientset, err := kubernetes.NewForConfig(kubeConfig)

	if err != nil {
		return nil, err
	}

	return clientset, nil
}

func getPods(ns string) (*vx.PodList, error) {
	clientset, err := kubectl("")

	pods, err := clientset.CoreV1().Pods(ns).List(context.Background(), v1.ListOptions{})

	if err != nil {
		return nil, err
	}

	return pods, nil

}

func deployKube() {

}

func getEvents(ns string) {

}

func main() {

	r := gin.Default()

	r.GET("/ping", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{
			"message": "pong",
		})
	})

	r.GET("/pods/:ns", func(c *gin.Context) {

		pods, err := getPods(c.Param("ns"))

		if err != nil {
			c.JSON(http.StatusOK, err)
			return
		}

		c.JSON(http.StatusOK, pods.Items)

	})

	if err := r.Run(":8556"); err != nil {
		return
	}
}
