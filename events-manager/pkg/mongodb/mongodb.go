package mongodbHandler

import (
	"context"
	"github.com/miladhzzzz/milx-cloud-init/events-manager/config"
	//"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"time"
)

func Dbc() (*mongo.Client, error) {
	cnf, err := config.ReadConfig()
	if err != nil {

	}

	//serverAPIOptions := options.ServerAPI(options.ServerAPIVersion1)
	clientOptions := options.Client().
		ApplyURI(cnf.MongoURI)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)

	defer cancel()

	//defer mongoAuditLog()

	return mongo.Connect(ctx, clientOptions)
}
