package mongodbHandler

import (
	"context"
	//"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"time"
)

func Dbc() (*mongo.Client, error) {
	//serverAPIOptions := options.ServerAPI(options.ServerAPIVersion1)
	clientOptions := options.Client().
		ApplyURI("mongodb://admin:admin@192.168.13.1:27017/?retryWrites=true&w=majority")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)

	defer cancel()

	//defer mongoAuditLog()

	return mongo.Connect(ctx, clientOptions)
}
