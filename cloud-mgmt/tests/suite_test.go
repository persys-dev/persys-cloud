package tests

import (
	"context"
	"github.com/persys-dev/persys-devops/cloud-mgmt/config"
	"github.com/persys-dev/persys-devops/cloud-mgmt/internal/cloud-provider/persys"
	pb "github.com/persys-dev/persys-devops/cloud-mgmt/proto"
	"github.com/persys-dev/persys-devops/cloud-mgmt/services"
	"github.com/stretchr/testify/suite"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"go.mongodb.org/mongo-driver/mongo/readpref"
	"google.golang.org/grpc"
	"testing"
)

type TestSuite struct {
	suite.Suite
	cloudService services.CloudService
	conn         *grpc.ClientConn
	mongoClient  *mongo.Client
}

// SetupTest initializes the test suite
func (suite *TestSuite) SetupTest() {
	config, err := config.ReadConfig()
	suite.Require().NoError(err)
	options := options.Client().ApplyURI(config.MongoURI)
	suite.mongoClient, err = mongo.Connect(context.Background(), options)
	suite.Require().NoError(err)
	err = suite.mongoClient.Ping(context.Background(), readpref.Primary())
	suite.Require().NoError(err)
	err = persys.CreateCluster()
	suite.Require().NoError(err)
	suite.cloudService = services.NewCloudService(suite.mongoClient.Database("cloud-mgmt"), context.Background())
	suite.conn, err = grpc.Dial("config.GrpcServerAddress", grpc.WithInsecure())
	suite.Require().NoError(err)
}

// TearDownTest cleans up after each test
func (suite *TestSuite) TearDownTest() {
	err := suite.conn.Close()
	suite.Require().NoError(err)
	err = suite.mongoClient.Disconnect(context.Background())
	suite.Require().NoError(err)
}

// TestCloudAction tests the CloudAction function
func (suite *TestSuite) TestCloudAction() {
	client := pb.NewCloudMgmtServiceClient(suite.conn)
	request := &pb.CloudRequest{Name: "test"}
	response, err := client.CloudAction(context.Background(), request)
	suite.Require().NoError(err)
	suite.Require().NotNil(response)
	suite.Equal(persys.CloudActionSuccess, response.Status)
	request = &pb.CloudRequest{Name: ""}
	response, err = client.CloudAction(context.Background(), request)
	suite.Require().NoError(err)
	suite.Require().NotNil(response)
	suite.Equal(persys.CloudActionFailure, response.Status)
}
func TestSuiteRunner(t *testing.T) {
	suite.Run(t, new(TestSuite))
}
