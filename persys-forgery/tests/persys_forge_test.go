package tests

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"github.com/gin-gonic/gin"
	"github.com/persys-dev/persys-cloud/persys-forgery/internal/build"
	"github.com/persys-dev/persys-cloud/persys-forgery/internal/db"
	"github.com/persys-dev/persys-cloud/persys-forgery/internal/handlers"
	"github.com/persys-dev/persys-cloud/persys-forgery/internal/models"
	"github.com/persys-dev/persys-cloud/persys-forgery/internal/secrets"
)

type mockOrchestrator struct{}

func (m *mockOrchestrator) BuildWithStrategy(ctx interface{}, req models.BuildRequest, strategy string) error {
	return nil
}

type mockSecretManager struct{}

func (m *mockSecretManager) Set(project, key, value string) error    { return nil }
func (m *mockSecretManager) Get(project, key string) (string, error) { return "mocked", nil }
func (m *mockSecretManager) List(project string) ([]string, error) {
	return []string{"key1", "key2"}, nil
}

type mockDocker struct{}

func (m *mockDocker) Build(ctx context.Context, req models.BuildRequest) error { return nil }

func init() {
	testDB, _ := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	db.DB = testDB
	// Migrate your models
	testDB.AutoMigrate(&models.Project{}, &models.Job{}, &models.ProjectSecret{})
}

func setupRouter(testDB *gorm.DB) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.Default()

	// Use the new orchestrator constructor
	orchestrator := build.NewOrchestrator("/tmp/test-forge-builds")
	orchestrator.SetDockerClient(&mockDocker{})

	buildHandler := handlers.NewBuildHandler(orchestrator)
	projectHandler := handlers.NewProjectHandler(testDB)
	secretHandler := handlers.NewSecretHandler(&secrets.Manager{DB: testDB})

	r.POST("/build", buildHandler.Build)
	r.GET("/status/:job_id", buildHandler.Status)
	r.POST("/projects", projectHandler.CreateProject)
	r.GET("/projects", projectHandler.ListProjects)
	r.GET("/projects/:project", projectHandler.GetProject)
	r.PUT("/projects/:project", projectHandler.UpdateProject)
	r.DELETE("/projects/:project", projectHandler.DeleteProject)
	r.POST("/secrets", secretHandler.SetSecret)
	// Use prefix for project secrets to avoid route conflict
	r.GET("/secrets/project/:project/:key", secretHandler.GetProjectSecret)
	r.GET("/secrets/project/:project", secretHandler.ListProjectSecrets)
	r.GET("/secrets/:key", secretHandler.GetSecret)
	return r
}

// Add a struct and a slice to collect test results
type testResult struct {
	label string
	got   int
	want  int
	pass  bool
}

var testResults []testResult

func assertWithEmoji(t *testing.T, got, want int, label string) {
	pass := got == want
	if pass {
		t.Logf("✅ %s: got %d as expected", label, got)
	} else {
		t.Errorf("❌ %s: got %d, want %d", label, got, want)
	}
	testResults = append(testResults, testResult{label, got, want, pass})
}

func TestPersysForge_AllEndpoints(t *testing.T) {
	testResults = nil // reset before running
	testDB, _ := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	testDB.AutoMigrate(&models.Project{}, &models.Job{}, &models.ProjectSecret{})
	r := setupRouter(testDB)

	// Test build (valid)
	buildPayload := models.BuildRequest{
		ProjectName: "test-proj",
		Type:        "dockerfile",
		Source:      "https://github.com/org/repo.git",
		CommitHash:  "abc123",
		Strategy:    "local",
		Metadata:    make(map[string]interface{}),
	}
	body, _ := json.Marshal(buildPayload)
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/build", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	assertWithEmoji(t, w.Code, http.StatusAccepted, "Build (valid)")

	// Test build (invalid payload)
	w = httptest.NewRecorder()
	req, _ = http.NewRequest("POST", "/build", bytes.NewBuffer([]byte(`{"foo":123}`)))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	assertWithEmoji(t, w.Code, http.StatusBadRequest, "Build (invalid payload)")

	// Test project create (valid)
	projPayload := models.Project{Name: "test-proj", RepoURL: "https://github.com/org/repo.git"}
	body, _ = json.Marshal(projPayload)
	w = httptest.NewRecorder()
	req, _ = http.NewRequest("POST", "/projects", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	assertWithEmoji(t, w.Code, http.StatusCreated, "Project create (valid)")

	// Test project create (duplicate)
	w = httptest.NewRecorder()
	req, _ = http.NewRequest("POST", "/projects", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	assertWithEmoji(t, w.Code, http.StatusConflict, "Project create (duplicate)")

	// Test get non-existent project
	w = httptest.NewRecorder()
	req, _ = http.NewRequest("GET", "/projects/doesnotexist", nil)
	r.ServeHTTP(w, req)
	assertWithEmoji(t, w.Code, http.StatusNotFound, "Get non-existent project")

	// Test update project (invalid payload)
	w = httptest.NewRecorder()
	req, _ = http.NewRequest("PUT", "/projects/test-proj", bytes.NewBuffer([]byte(`{"bad":}`)))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	assertWithEmoji(t, w.Code, http.StatusBadRequest, "Update project (invalid payload)")

	// Test delete non-existent project
	w = httptest.NewRecorder()
	req, _ = http.NewRequest("DELETE", "/projects/doesnotexist", nil)
	r.ServeHTTP(w, req)
	assertWithEmoji(t, w.Code, http.StatusOK, "Delete non-existent project")

	// Test secret set (valid)
	secretPayload := map[string]string{"key": "foo", "value": "bar"}
	body, _ = json.Marshal(secretPayload)
	w = httptest.NewRecorder()
	req, _ = http.NewRequest("POST", "/secrets", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	assertWithEmoji(t, w.Code, http.StatusOK, "Secret set (valid)")

	// Test secret set (invalid payload)
	w = httptest.NewRecorder()
	req, _ = http.NewRequest("POST", "/secrets", bytes.NewBuffer([]byte(`{"bad":}`)))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	assertWithEmoji(t, w.Code, http.StatusBadRequest, "Secret set (invalid payload)")

	// Test get non-existent secret
	w = httptest.NewRecorder()
	req, _ = http.NewRequest("GET", "/secrets/doesnotexist", nil)
	r.ServeHTTP(w, req)
	assertWithEmoji(t, w.Code, http.StatusNotFound, "Get non-existent secret")

	// Test status for non-existent job
	w = httptest.NewRecorder()
	req, _ = http.NewRequest("GET", "/status/9999", nil)
	r.ServeHTTP(w, req)
	assertWithEmoji(t, w.Code, http.StatusNotFound, "Status for non-existent job")

	// Print summary at the end
	t.Logf("\nTest Summary:")
	for _, res := range testResults {
		if res.pass {
			t.Logf("✅ %s: got %d as expected", res.label, res.got)
		} else {
			t.Logf("❌ %s: got %d, want %d", res.label, res.got, res.want)
		}
	}
}
