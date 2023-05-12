package services

import (
	"context"
	"github.com/google/go-github/github"
	"github.com/miladhzzzz/milx-cloud-init/api-gateway/models"
	"time"

	"go.mongodb.org/mongo-driver/mongo"
)

type GithubServiceImpl struct {
	collection *mongo.Collection
	ctx        context.Context
}

func (g GithubServiceImpl) SetAccessToken() {
	//TODO implement me
	panic("implement me")
}

func (g GithubServiceImpl) ListRepos() {
	//TODO implement me
	panic("implement me")
}

func (g GithubServiceImpl) SetWebhook() {
	//TODO implement me
	panic("implement me")
}

func (g GithubServiceImpl) GetRepos(client *github.Client) error {
	repos, _, _ := client.Repositories.List(context.Background(), "", &github.RepositoryListOptions{})

	for _, repo := range repos {
		data := models.Repos{
			RepoID:      *repo.ID,
			GitURL:      *repo.GitURL,
			Name:        *repo.Name,
			Owner:       repo.Owner.String(),
			UserID:      0,
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
	return nil
}

func NewGithubService(collection *mongo.Collection, ctx context.Context) GithubService {
	return &GithubServiceImpl{collection, ctx}
}
