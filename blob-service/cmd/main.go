package main

import (
	"archive/zip"
	"bytes"
	"github.com/gin-gonic/gin"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"

	"github.com/persys-dev/persys-cloud/blob-service/config"
	"github.com/persys-dev/persys-cloud/blob-service/internal/git"
)

type req struct {
	Url     string `json:"url"`
	Private string `json:"private"`
	Token   string `json:"token"`
}

var (
	server *gin.Engine
	cnf    *config.Config
	dst    = "/artifacts/"
)

func init() {

	cnf, _ = config.ReadConfig()
	server = gin.Default()

}

func zipRepo(repoPath string) (*bytes.Buffer, error) {
	buf := new(bytes.Buffer)
	zipWriter := zip.NewWriter(buf)

	err := filepath.Walk(repoPath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		if info.IsDir() {
			return nil
		}

		relPath, err := filepath.Rel(repoPath, path)
		if err != nil {
			return err
		}

		zipFile, err := zipWriter.Create(relPath)
		if err != nil {
			return err
		}

		file, err := os.Open(path)
		if err != nil {
			return err
		}
		defer file.Close()

		_, err = io.Copy(zipFile, file)
		return err
	})

	if err != nil {
		return nil, err
	}

	err = zipWriter.Close()
	if err != nil {
		return nil, err
	}

	return buf, nil
}

func main() {

	startGinServer()

}

func startGinServer() {

	logFile, _ := os.Create("blob-service-http.log")

	if cnf == nil {
		panic("config not loaded")
	}

	router := server.Group("/api/v1alpha")

	router.Use(gin.LoggerWithWriter(logFile))

	router.StaticFS("/artifacts", http.Dir("/artifacts"))

	server.MaxMultipartMemory = 8 << 20 // 8 MiB

	router.POST("/upload", func(c *gin.Context) {
		// single file
		file, _ := c.FormFile("file")
		log.Println(file.Filename)
		// get username from URL parameters
		name := c.Query("user")

		if name == "" {
			//log.Printf("username is empty")
			//return
			name = "default"
		}

		folder := dst + name + "/" + file.Filename
		// Upload the file to specific dst.
		err := c.SaveUploadedFile(file, folder)

		if err != nil {
			c.JSON(http.StatusBadRequest, err)
			return
		}

		url := "http://blob-service:8552/api/v1alpha/artifacts/" + name + "/" + file.Filename
		c.JSON(http.StatusOK, gin.H{"Download": url})
	})

	router.POST("/git/clone", func(c *gin.Context) {

		var request *req

		c.BindJSON(&request)

		url := request.Url
		private := request.Private
		token := request.Token

		var pv = false

		if private == "true" {
			if token == "" {
				c.JSON(400, "your repo is private but you didnt provide your access token!")
				return
			}
		} else if private == "true" {
			pv = true
		}

		commit, s, err := git.Gits(url, pv, token)

		if err != nil {
			c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			//log.Printf("couldnt clone err: %v", err)
			return
		}
		//fmt.Print(s)

		c.JSON(http.StatusOK, gin.H{
			"directory": s,
			"hash":      commit.Hash.String(),
			"owner":     commit.Author,
			"url":       "http://localhost:8552/api/v1alpha" + s,
			"zip":       "http://localhost:8552/api/v1alpha/download" + "/" + git.ExtractUsernameRepo(url),
		})

	})

	router.GET("/download/:username/:repo", func(c *gin.Context) {
		username := c.Param("username")
		repo := c.Param("repo")
		repoPath := filepath.Join("/artifacts/git", username, repo)

		zipBuffer, err := zipRepo(repoPath)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}

		c.Header("Content-Disposition", "attachment; filename="+repo+".zip")
		c.Data(http.StatusOK, "application/zip", zipBuffer.Bytes())
	})

	log.Fatal(server.Run(cnf.HttpAddr))
}
