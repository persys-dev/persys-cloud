package providers

import (
	"context"
	"fmt"
	"github.com/persys-dev/persys-cloud/cloud-mgmt/utils"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"log"
	capvv1beta1 "sigs.k8s.io/cluster-api-provider-vsphere/apis/v1beta1"
	capi "sigs.k8s.io/cluster-api/api/v1beta1"
)

var (
	kubeConfig *rest.Config
)

func getKubeConfig() *kubernetes.Clientset {
	var err error

	err = utils.DownloadFile("persys", "kube-config.yaml")

	if err != nil {
		log.Fatalf("kube config didnt download")
	}

	config, err := clientcmd.BuildConfigFromFlags("", "/kube-config.yaml")

	config, err = rest.InClusterConfig()

	if err != nil {
		log.Fatalf("Error creating Kubernetes config: %v\n", err)
	}

	clientSet, err := kubernetes.NewForConfig(config)

	if err != nil {
		log.Fatalf("Error creating Kubernetes client: %v\n", err)
	}

	return clientSet
}

func CreateCluster() error {
	clientset := getKubeConfig()

	// Register the CAPV types with the Kubernetes scheme
	if err := capvv1beta1.AddToScheme(scheme.Scheme); err != nil {
		fmt.Printf("Error adding CAPV to scheme: %v\n", err)
		return err
	}
	// Create a new CAPV VSphereCluster object
	vsphereCluster := &capvv1beta1.VSphereCluster{
		TypeMeta: metav1.TypeMeta{
			APIVersion: capvv1beta1.GroupVersion.String(),
			Kind:       "VSphereCluster",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "my-cluster",
			Namespace: "default",
		},
		Spec: capvv1beta1.VSphereClusterSpec{},
	}
	// Create the CAPI kubernetes cluster object
	cluster := &capi.Cluster{
		TypeMeta: metav1.TypeMeta{
			APIVersion: capi.GroupVersion.String(),
			Kind:       "Cluster",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "my-cluster",
			Namespace: "default",
		},
		Spec: capi.ClusterSpec{},
	}
	// Create the cluster using the Kubernetes API
	err := createOrUpdateCluster(context.Background(), clientset, vsphereCluster, cluster)
	if err != nil {
		fmt.Printf("Error creating or updating cluster: %v\n", err)
		return err
	}

	fmt.Printf("Cluster created or updated: %s\n", cluster.Name)

	return nil
}

func createOrUpdateCluster(ctx context.Context, clientset *kubernetes.Clientset, vsphereCluster *capvv1beta1.VSphereCluster, cluster *capi.Cluster) error {
	client := clientset.RESTClient()
	// Encode the VSphereCluster and Cluster objects using the runtime encoder
	vsphereClusterData, err := runtime.Encode(scheme.Codecs.LegacyCodec(capvv1beta1.GroupVersion), vsphereCluster)
	if err != nil {
		return fmt.Errorf("failed to encode VSphereCluster: %v", err)
	}
	clusterData, err := runtime.Encode(scheme.Codecs.LegacyCodec(capi.GroupVersion), cluster)
	if err != nil {
		return fmt.Errorf("failed to encode Cluster: %v", err)
	}
	// Send a POST request to create the VSphereCluster object
	err = client.Post().
		Namespace(vsphereCluster.Namespace).
		Resource("vsphereclusters").
		Body(vsphereClusterData).
		Do(ctx).
		Error()
	if errors.IsAlreadyExists(err) {
		// If the VSphereCluster object already exists, send a PUT request to update it
		err = client.Put().
			Namespace(vsphereCluster.Namespace).
			Resource("vsphereclusters").
			Name(vsphereCluster.Name).
			Body(vsphereClusterData).
			Do(ctx).
			Error()
	}
	if err != nil {
		return fmt.Errorf("failed to create or update VSphereCluster: %v", err)
	}
	// Send a POST request to create the Cluster object
	err = client.Post().
		Namespace(cluster.Namespace).
		Resource("clusters").
		Body(clusterData).
		Do(ctx).
		Error()
	if errors.IsAlreadyExists(err) {
		// If the Cluster object already exists, send a PUT request to update it
		err = client.Put().
			Namespace(cluster.Namespace).
			Resource("clusters").
			Name(cluster.Name).
			Body(clusterData).
			Do(ctx).
			Error()
	}
	if err != nil {
		return fmt.Errorf("failed to create or update Cluster: %v", err)
	}
	return nil
}
