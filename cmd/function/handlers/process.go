package handlers

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
)

// Replace this function with the application's processing logic.
func process(c *gin.Context, data []byte) {
	started := c.MustGet("started").(time.Time)
	format := map[string]string{"/api/json": "json", "/api/jsonzip": "json", "/api/bin": "multipart/form-data", "/api/binzip": "multipart/form-data", "/api/proto": "protobuf", "/api/protozip": "protobuf"}[c.FullPath()]
	encoding := c.GetHeader("Content-Encoding")
	if encoding == "" {
		encoding = "identity"
	}
	metrics := gin.H{
		"timestamp":        started.Format("2006-01-02T15:04:05Z07:00"),
		"endpoint":         c.FullPath(),
		"format":           format,
		"content_type":     c.GetHeader("Content-Type"),
		"content_encoding": encoding,
		"request_bytes":    c.GetInt("request_bytes"),
		"data_bytes":       len(data),
		"elapsed_ns":       time.Since(started).Nanoseconds(),
	}
	if format == "json" {
		metrics["data_encoding"] = "base64"
	}
	if size, ok := c.Get("protobuf_bytes"); ok {
		metrics["protobuf_bytes"] = size
	}
	if filename, ok := c.Get("filename"); ok {
		metrics["filename"] = filename
		metrics["file_content_type"] = c.GetString("file_content_type")
	}
	response := gin.H{"metrics": metrics}
	if payload, ok := c.Get("payload"); ok {
		response["payload"] = payload
	}
	c.JSON(http.StatusOK, response)
}

func processBatch(c *gin.Context, batch []*payloadInfo, format string) {
	started := c.MustGet("started").(time.Time)
	encoding := c.GetHeader("Content-Encoding")
	if encoding == "" {
		encoding = "identity"
	}
	totalData := 0
	for _, p := range batch {
		totalData += len(p.Payload)
	}
	metrics := gin.H{
		"timestamp":        started.Format("2006-01-02T15:04:05Z07:00"),
		"endpoint":         c.FullPath(),
		"format":           format,
		"content_type":     c.GetHeader("Content-Type"),
		"content_encoding": encoding,
		"request_bytes":    c.GetInt("request_bytes"),
		"batch_entries":    len(batch),
		"total_data_bytes": totalData,
		"elapsed_ns":       time.Since(started).Nanoseconds(),
	}
	if format == "json" {
		metrics["data_encoding"] = "base64"
	}
	if size, ok := c.Get("protobuf_bytes"); ok {
		metrics["protobuf_bytes"] = size
	}
	response := gin.H{
		"metrics": metrics,
		"batch":   batch,
	}
	c.JSON(http.StatusOK, response)
}
