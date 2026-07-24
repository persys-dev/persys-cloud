package controllers

import (
	"io"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

func decodeProtoBody(ctx *gin.Context, msg proto.Message) bool {
	body, err := io.ReadAll(ctx.Request.Body)
	if err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "failed to read request body"})
		return false
	}
	if len(strings.TrimSpace(string(body))) == 0 {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "request body is required"})
		return false
	}
	unmarshal := protojson.UnmarshalOptions{DiscardUnknown: true}
	if err := unmarshal.Unmarshal(body, msg); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "invalid request payload"})
		return false
	}
	return true
}

func writeProtoJSON(ctx *gin.Context, status int, msg proto.Message) {
	data, err := protojson.MarshalOptions{UseProtoNames: true}.Marshal(msg)
	if err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": "failed to encode response"})
		return
	}
	ctx.Data(status, "application/json", data)
}
