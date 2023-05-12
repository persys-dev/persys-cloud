package main

import (
	"github.com/gin-gonic/gin"
	"log"
	"net/http"
	"os"

	"github.com/miladhzzzz/milx-cloud-init/blob-service/config"
	"github.com/miladhzzzz/milx-cloud-init/blob-service/internal/git"
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
			log.Fatalf("couldnt clone err: %v", err)
		}
		//fmt.Print(s)

		c.JSON(http.StatusOK, gin.H{
			"directory": s,
			"hash":      commit.Hash.String(),
			"owner":     commit.Author,
			"url":       "http://blob-service:8552/api/v1alpha" + s,
		})

	})

	log.Fatal(server.Run(cnf.HttpAddr))
}
