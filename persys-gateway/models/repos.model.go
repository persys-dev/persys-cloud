package models

type Repos struct {
	RepoID      int64  `json:"repoID" bson:"repoID"`
	GitURL      string `json:"gitURL" bson:"gitURL"`
	Name        string `json:"name" bson:"name"`
	Owner       string `json:"owner" bson:"owner"`
	UserID      int64  `json:"userID" bson:"userID"`
	Private     bool   `json:"private" bson:"private"`
	AccessToken string `json:"accessToken" bson:"accessToken"`
	WebhookURL  string `json:"webhookURL" bson:"webhookURL"`
	EventID     int64  `json:"eventID" bson:"eventID"`
	CreatedAt   string `json:"createdAt" bson:"createdAt"`
}
