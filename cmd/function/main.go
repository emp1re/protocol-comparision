package main

import (
	"bytes"
	"io"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/aws/aws-lambda-go/lambda"
	ginadapter "github.com/awslabs/aws-lambda-go-api-proxy/gin"
	"github.com/emp1re/protocol-comparision/cmd/function/handlers"
	"github.com/gin-gonic/gin"
)

func main() {
	gin.SetMode(gin.ReleaseMode)
	if len(os.Args) > 1 && os.Args[1] == "--local" {
		log.Println("Running locally on http://127.0.0.1:8080")

		r := newRouter()
		r.Run("127.0.0.1:8080")

		return
	}
	adapter := ginadapter.NewV2(newRouter())
	lambda.Start(adapter.ProxyWithContext)
}

func newRouter() *gin.Engine {
	r := gin.New()
	r.Use(gin.Logger())
	r.Use(gin.Recovery())
	r.Use(func(c *gin.Context) {
		c.Set("started", time.Now())
		body, err := io.ReadAll(io.LimitReader(c.Request.Body, handlers.MaxBodySize+1))
		if err != nil || len(body) > handlers.MaxBodySize {
			c.AbortWithStatusJSON(http.StatusRequestEntityTooLarge, gin.H{"error": "invalid or oversized request body"})
			return
		}
		c.Set("request_bytes", len(body))
		c.Request.Body = io.NopCloser(bytes.NewReader(body))
		c.Next()
	})
	r.POST("/api/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})

	r.POST("/api/proto", handlers.ProtoHandler)
	r.POST("/api/protozip", handlers.ProtoHandler)
	r.POST("/api/protobatch", handlers.ProtoBatchHandler)
	r.POST("/api/protobatchzip", handlers.ProtoBatchHandler)

	r.POST("/api/json", handlers.JsonHandler)
	r.POST("/api/jsonzip", handlers.JsonHandler)
	r.POST("/api/jsonbatch", handlers.JsonBatchHandler)
	r.POST("/api/jsonbatchzip", handlers.JsonBatchHandler)

	r.POST("/api/bin", handlers.BinHandler)
	r.POST("/api/binzip", handlers.BinHandler)
	r.POST("/api/binbatch", handlers.BinBatchHandler)
	r.POST("/api/binbatchzip", handlers.BinBatchHandler)
	return r
}
