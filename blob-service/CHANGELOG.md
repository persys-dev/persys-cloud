# Change Log


All notable changes to this project will be documented in this file

* [2/23/2023] started the project using standard api development for an in-cluster artifact storage service using gin.
* [2/24/2023] it's a very clean api that has two endpoints : /artifacts/{username}/{filename} for download and /upload?user={username} for uploading files to specific user directory which returns a JSON payload with the exact download url for that artifact.