package services

import (
	"context"
	"github.com/google/go-github/github"
	"github.com/miladhzzzz/milx-cloud-init/api-gateway/models"
	"go.mongodb.org/mongo-driver/bson"
	"time"

	"go.mongodb.org/mongo-driver/mongo"
)

type GithubServiceImpl struct {
	collection *mongo.Collection
	ctx        context.Context
}

func NewGithubService(collection *mongo.Collection, ctx context.Context) GithubService {
	return &GithubServiceImpl{collection: collection, ctx: ctx}
}

func (g *GithubServiceImpl) SetAccessToken() {
	//TODO implement me
	panic("implement me")
}

func (g *GithubServiceImpl) ListRepos(user *models.DBResponse) (*bson.M, error) {
	// Find all repos owned by the user
	repos, err := g.collection.Find(g.ctx, bson.M{"owner": user.Login})
	if err != nil {
		// Handle error
		return nil, err
	}

	// Retrieve all results
	var results []bson.M
	err = repos.All(g.ctx, &results)
	if err != nil {
		// Handle error
		return nil, err
	}

	// Combine all results into a single bson.M object
	result := bson.M{}
	for _, r := range results {
		for k, v := range r {
			result[k] = v
		}
	}

	return &result, nil
}

func (g *GithubServiceImpl) SetWebhook() {
	//TODO implement me
	panic("implement me")
}

func (g *GithubServiceImpl) GetRepos(client *github.Client, user *models.UserInput) error {
	repos, _, _ := client.Repositories.List(context.Background(), "", &github.RepositoryListOptions{})

	//totalRepo := len(repos)

	// check if repos exist don't copy to db
	r := g.collection.FindOne(g.ctx, bson.M{"name": repos[2].Name})

	if r.Err() == mongo.ErrNoDocuments {
		for _, repo := range repos {
			data := models.Repos{
				RepoID:      *repo.ID,
				GitURL:      *repo.GitURL,
				Name:        *repo.Name,
				Owner:       user.Login,
				UserID:      user.UserID,
				Private:     *repo.Private,
				AccessToken: "",
				WebhookURL:  "",
				EventID:     0,
				CreatedAt:   time.Now().String(),
			}
			_, err := g.collection.InsertOne(g.ctx, &data)

			if err != nil {
				return err
			}
		}
	}
	return nil
}
