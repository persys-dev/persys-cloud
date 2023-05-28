package azure

//
//import (
//	"context"
//	"log"
//
//	"github.com/Azure/azure-sdk-for-go/profiles/latest/containerservice/mgmt/containerservice"
//	"github.com/Azure/go-autorest/autorest/azure/auth"
//)
//
//func main() {
//	authorizer, err := auth.NewAuthorizerFromEnvironment()
//	if err != nil {
//		log.Fatalf("Failed to get OAuth config: %v", err)
//	}
//
//	client := containerservice.NewManagedClustersClient("<your-subscription-id>")
//	client.Authorizer = authorizer
//
//	resourceGroupName := "<your-resource-group-name>"
//	clusterName := "<your-cluster-name>"
//
//	adminCredentials, err := client.ListClusterAdminCredentials(context.Background(), resourceGroupName, clusterName)
//	if err != nil {
//		log.Fatalf("Failed to list cluster admin credentials: %v", err)
//	}
//
//	log.Printf("Cluster admin username: %s", *adminCredentials.Kubeconfigs[0].Username)
//	log.Printf("Cluster admin password: %s", *adminCredentials.Kubeconfigs[0].Password)
//}
