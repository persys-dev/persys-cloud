package utils

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"io/ioutil"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
)

const (
	baseURL       = "http://localhost:8552/api/v1alpha/"
	baseUploadURL = "http://localhost:8552/api/v1alpha/upload/"
)

// Req defines the request structure.
type Req struct {
	URL     string `json:"url"`
	Private string `json:"private"`
	Token   string `json:"token"`
}

var (
	client = &http.Client{}
)

// UploadFile uploads a file to the specified destination.
func UploadFile(user string, file io.Reader, filename string) (string, error) {
	var err error
	if user == "" {
		user = "default"
	}
	url := baseUploadURL + "?" + user
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	part, err := writer.CreateFormFile("file", filename)
	if err != nil {
		return "", err
	}
	_, err = io.Copy(part, file)
	if err != nil {
		return "", err
	}
	writer.Close()
	req, err := http.NewRequest("POST", url, body)
	if err != nil {
		return "", err
	}
	req.Header.Add("Content-Type", writer.FormDataContentType())

	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", errors.New(resp.Status)
	}
	respBody, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	return baseURL + string(respBody), nil
}

// CloneRepo clones a git repository.
func CloneRepo(url string, private string, token string) (map[string]interface{}, error) {
	var reqs = Req{
		URL:     url,
		Private: private,
		Token:   token,
	}
	jsonReq, err := json.Marshal(reqs)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequest("POST", baseURL+"git/clone", bytes.NewBuffer(jsonReq))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, errors.New(resp.Status)
	}
	respBody, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	var result map[string]interface{}
	err = json.Unmarshal(respBody, &result)
	if err != nil {
		return nil, err
	}
	return result, nil
}

// DownloadRepo downloads a git repository.
func DownloadRepo(username string, repo string) (string, error) {
	resp, err := client.Get(baseURL + "download/" + username + "/" + repo)
	if err != nil {
		return "nil", err
	}

	defer resp.Body.Close()

	// Create the output directory if it doesn't exist
	outputDir := username

	err = os.MkdirAll(outputDir, os.ModePerm)
	if err != nil {

	}

	// Create a temporary file to write the zip data to
	tmpFile, err := os.CreateTemp(outputDir, "tmp*.zip")
	if err != nil {

	}
	defer tmpFile.Close()
	defer os.Remove(tmpFile.Name())

	// Write the zip data to the temporary file
	_, err = io.Copy(tmpFile, resp.Body)
	if err != nil {

	}

	// Open the zip file
	r, err := zip.OpenReader(tmpFile.Name())
	if err != nil {

	}
	defer r.Close()

	// Extract the files from the zip to the output directory
	for _, f := range r.File {
		path := filepath.Join(outputDir, f.Name)
		if f.FileInfo().IsDir() {
			err = os.MkdirAll(path, f.Mode())
			if err != nil {

			}
			continue
		}
		err = os.MkdirAll(filepath.Dir(path), os.ModePerm)
		if err != nil {

		}
		dst, err := os.Create(path)
		if err != nil {

		}
		rc, err := f.Open()
		if err != nil {

		}
		_, err = io.Copy(dst, rc)
		if err != nil {

		}
		rc.Close()
		dst.Close()
	}
	if resp.StatusCode != http.StatusOK {
		return "", errors.New(resp.Status)
	}
	return username, nil
}
