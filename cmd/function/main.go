package main

import (
	"bytes"
	"compress/gzip"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/aws/aws-lambda-go/lambda"
	ginadapter "github.com/awslabs/aws-lambda-go-api-proxy/gin"
	"github.com/gin-gonic/gin"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type payloadInfo struct {
	Payload []byte     `json:"-"`
	Time    *time.Time `json:"time,omitempty"`
	TimeEnd *time.Time `json:"time_end,omitempty"`
	Type    string     `json:"type"`
}

// The entire HTTP request, including multipart overhead, must fit this limit.
const maxBodySize = 4 << 20

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
		body, err := io.ReadAll(io.LimitReader(c.Request.Body, maxBodySize+1))
		if err != nil || len(body) > maxBodySize {
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
	r.POST("/api/proto", protoHandler)
	r.POST("/api/protozip", protoHandler)
	r.POST("/api/protobatch", protoBatchHandler)
	r.POST("/api/protobatchzip", protoBatchHandler)
	r.POST("/api/json", jsonHandler)
	r.POST("/api/jsonzip", jsonHandler)

	r.POST("/api/bin", binHandler)
	r.POST("/api/binzip", binHandler)
	return r
}

func binHandler(c *gin.Context) {
	encoding := strings.TrimSpace(c.GetHeader("Content-Encoding"))

	if strings.EqualFold(encoding, "gzip") {
		gz, err := gzip.NewReader(c.Request.Body)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "expected gzip-compressed multipart body"})
			return
		}
		defer gz.Close()

		body, err := io.ReadAll(io.LimitReader(gz, maxBodySize+1))
		if err != nil || len(body) > maxBodySize {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid or oversized multipart body"})
			return
		}

		c.Request.Body = io.NopCloser(bytes.NewReader(body))

	} else if encoding != "" && !strings.EqualFold(encoding, "identity") {
		c.JSON(http.StatusUnsupportedMediaType, gin.H{"error": "unsupported content encoding"})
		return
	}

	reader, err := c.Request.MultipartReader()
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "expected multipart/form-data"})
		return
	}

	payload := &payloadInfo{}

	foundData := false

	for {
		part, err := reader.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid multipart body"})
			return
		}
		value, err := io.ReadAll(part)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid multipart field"})
			return
		}
		fmt.Println("Name", part.FormName())
		switch part.FormName() {
		case "file", "payload":
			payload.Payload = value
			foundData = true
			if part.FileName() != "" {
				c.Set("filename", part.FileName())
				c.Set("file_content_type", part.Header.Get("Content-Type"))
			}
		case "type":
			payload.Type = string(value)
		case "time", "timeEnd", "time_end":
			t, err := time.Parse(time.RFC3339Nano, string(value))
			if err != nil {
				c.JSON(http.StatusBadRequest, gin.H{"error": "invalid time format"})
				return
			}
			if part.FormName() == "time" {
				payload.Time = &t
			} else {
				payload.TimeEnd = &t
			}
		}
	}

	if !foundData {
		c.JSON(http.StatusBadRequest, gin.H{"error": "multipart file or payload field is required"})
		return
	}

	c.Set("payload", payload)
	process(c, payload.Payload)
}

func jsonHandler(c *gin.Context) {
	var body struct {
		Payload *string    `json:"payload"`
		Time    *time.Time `json:"time"`
		TimeEnd *time.Time `json:"time_end"`
		Type    string     `json:"type"`
	}

	encoding := strings.TrimSpace(c.GetHeader("Content-Encoding"))
	if strings.EqualFold(encoding, "gzip") {
		gz, err := gzip.NewReader(c.Request.Body)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "expected gzip-compressed JSON"})
			return
		}
		defer gz.Close()
		c.Request.Body = io.NopCloser(gz)
	}
	if err := c.ShouldBindJSON(&body); err != nil || body.Payload == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "expected JSON with a base64 data field"})
		return
	}

	data, err := base64.StdEncoding.Strict().DecodeString(*body.Payload)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "data must be valid standard base64"})
		return
	}

	c.Set("payload", &payloadInfo{Payload: data, Time: body.Time, TimeEnd: body.TimeEnd, Type: body.Type})
	process(c, data)
}

func protoHandler(c *gin.Context) {
	var reader io.Reader = c.Request.Body

	encoding := strings.TrimSpace(c.GetHeader("Content-Encoding"))
	if strings.EqualFold(encoding, "gzip") {
		gz, err := gzip.NewReader(reader)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "expected gzip-compressed protobuf"})
			return
		}
		defer gz.Close()
		reader = gz
	} else if encoding != "" && !strings.EqualFold(encoding, "identity") {
		c.JSON(http.StatusUnsupportedMediaType, gin.H{"error": "unsupported content encoding"})
		return
	}

	body, err := io.ReadAll(io.LimitReader(reader, maxBodySize+1))
	if err != nil || len(body) > maxBodySize {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid or oversized protobuf body"})
		return
	}

	c.Set("protobuf_bytes", len(body))

	payload, err := decodeProto(body)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid protobuf body"})
		return
	}

	c.Set("payload", payload)

	process(c, payload.Payload)
}

func protoBatchHandler(c *gin.Context) {
	var reader io.Reader = c.Request.Body

	encoding := strings.TrimSpace(c.GetHeader("Content-Encoding"))
	if strings.EqualFold(encoding, "gzip") {
		gz, err := gzip.NewReader(reader)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "expected gzip-compressed protobuf"})
			return
		}
		defer gz.Close()
		reader = gz
	} else if encoding != "" && !strings.EqualFold(encoding, "identity") {
		c.JSON(http.StatusUnsupportedMediaType, gin.H{"error": "unsupported content encoding"})
		return
	}

	body, err := io.ReadAll(io.LimitReader(reader, maxBodySize+1))
	if err != nil || len(body) > maxBodySize {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid or oversized protobuf body"})
		return
	}

	c.Set("protobuf_bytes", len(body))

	batch, err := decodeProtoBatch(body)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid protobuf batch"})
		return
	}

	c.Set("batch_entries", len(batch))
	var totalData int64
	for _, p := range batch {
		totalData += int64(len(p.Payload))
	}
	c.Set("total_data_bytes", totalData)
	
	processBatch(c, batch)
}

// Decode Payload from proto/input.proto, ignoring unknown fields.
func decodeProto(body []byte) (*payloadInfo, error) {
	payload := &payloadInfo{}
	for len(body) > 0 {
		num, typ, n := protowire.ConsumeTag(body)
		if n < 0 {
			return nil, protowire.ParseError(n)
		}
		body = body[n:]
		if num >= 1 && num <= 4 && typ == protowire.BytesType {
			value, size := protowire.ConsumeBytes(body)
			if size < 0 {
				return nil, protowire.ParseError(size)
			}
			switch num {
			case 1:
				if !utf8.Valid(value) {
					return nil, errors.New("invalid UTF-8")
				}
				payload.Type = string(value)
			case 2, 3:
				var timestamp timestamppb.Timestamp
				if err := proto.Unmarshal(value, &timestamp); err != nil {
					return nil, err
				}
				if err := timestamp.CheckValid(); err != nil {
					return nil, err
				}
				t := timestamp.AsTime()
				if num == 2 {
					payload.Time = &t
				} else {
					payload.TimeEnd = &t
				}
			case 4:
				payload.Payload = value
			}
			body = body[size:]
			continue
		}
		size := protowire.ConsumeFieldValue(num, typ, body)
		if size < 0 {
			return nil, protowire.ParseError(size)
		}
		body = body[size:]
	}
	return payload, nil
}

// Decode Batch from proto/input.proto containing multiple Payload entries.
func decodeProtoBatch(body []byte) ([]*payloadInfo, error) {
	var batch []*payloadInfo
	
	for len(body) > 0 {
		num, typ, n := protowire.ConsumeTag(body)
		if n < 0 {
			return nil, protowire.ParseError(n)
		}
		body = body[n:]
		
		if num == 1 && typ == protowire.BytesType {
			value, size := protowire.ConsumeBytes(body)
			if size < 0 {
				return nil, protowire.ParseError(size)
			}
			payload, err := decodeProto(value)
			if err != nil {
				return nil, err
			}
			batch = append(batch, payload)
			body = body[size:]
		} else {
			size := protowire.ConsumeFieldValue(num, typ, body)
			if size < 0 {
				return nil, protowire.ParseError(size)
			}
			body = body[size:]
		}
	}
	return batch, nil
}

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

func processBatch(c *gin.Context, batch []*payloadInfo) {
	started := c.MustGet("started").(time.Time)
	format := "protobuf"
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
		"batch_entries":    c.GetInt("batch_entries"),
		"total_data_bytes": c.GetInt("total_data_bytes"),
		"elapsed_ns":       time.Since(started).Nanoseconds(),
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
