package models

type CliReq struct {
	State string `json:"state"`
}

type UserInput struct {
	Login       string `json:"login"`
	Name        string `json:"name"`
	Email       string `json:"email"`
	Company     string `json:"company"`
	URL         string `json:"url"`
	GithubToken string `json:"githubToken"`
	UserID      int64  `json:"userID"`
	PersysToken string `json:"persysToken"`
	State       string `json:"state"`
	Status      string `json:"status"`
	CreatedAt   string `json:"createdAt"`
	UpdatedAt   string `json:"updatedAt"`
}

type DBResponse struct {
	Login       string `json:"login"`
	Name        string `json:"name"`
	Email       string `json:"email"`
	Company     string `json:"company"`
	URL         string `json:"url"`
	GithubToken string `json:"githubToken"`
	UserID      int64  `json:"userID"`
	PersysToken string `json:"persysToken"`
	State       string `json:"state"`
	Status      string `json:"status"`
	CreatedAt   string `json:"createdAt"`
	UpdatedAt   string `json:"updatedAt"`
}

// UserResponse (an ObjectID-keyed view, presumably meant for some future
// admin/listing endpoint) was defined but never referenced anywhere in
// the codebase — dropped rather than migrated. It also depended on
// mongo-driver's primitive.ObjectID, which the gateway no longer imports
// at all now that Postgres has replaced MongoDB.
