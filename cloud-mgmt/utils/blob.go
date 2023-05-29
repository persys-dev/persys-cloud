package utils

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"mime/multipart"
	"net/http"
)

const (
	artifacts     = "http://localhost:8552/api/v1alpha/artifacts"
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

func DownloadFile(user string, filename string) error {
	url := artifacts + "/" + user + "/" + filename
	resp, err := http.Get(url)
	if err != nil {
		return fmt.Errorf("error: %v", err)
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)

	if err != nil {
		return fmt.Errorf("error reading response body: %v", err)
	}

	err = ioutil.WriteFile("kube-config.yaml", data, 0644)
	if err != nil {
		return fmt.Errorf("error writing file: %v", err)
	}
	return nil
}

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
func DownloadRepo(username string, repo string) (io.ReadCloser, error) {
	resp, err := client.Get(baseURL + "download/" + username + "/" + repo)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, errors.New(resp.Status)
	}
	return resp.Body, nil
}
