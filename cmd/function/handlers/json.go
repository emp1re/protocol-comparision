package handlers

import (
	"compress/gzip"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

const MaxBodySize = 20 << 20

func JsonHandler(c *gin.Context) {
	var body jsonPayload

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

func JsonBatchHandler(c *gin.Context) {
	var reader io.Reader = c.Request.Body
	encoding := strings.TrimSpace(c.GetHeader("Content-Encoding"))
	if strings.EqualFold(encoding, "gzip") {
		gz, err := gzip.NewReader(reader)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "expected gzip-compressed JSON"})
			return
		}
		defer gz.Close()
		reader = gz
	} else if encoding != "" && !strings.EqualFold(encoding, "identity") {
		c.JSON(http.StatusUnsupportedMediaType, gin.H{"error": "unsupported content encoding"})
		return
	}

	body, err := io.ReadAll(io.LimitReader(reader, MaxBodySize+1))
	if err != nil || len(body) > MaxBodySize {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid or oversized JSON body"})
		return
	}
	var input struct {
		Entries []jsonPayload `json:"entries"`
	}
	if err := json.Unmarshal(body, &input); err != nil || input.Entries == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "expected JSON with an entries array"})
		return
	}
	batch := make([]*payloadInfo, 0, len(input.Entries))
	for _, entry := range input.Entries {
		if entry.Payload == nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "each entry requires a base64 payload"})
			return
		}
		data, err := base64.StdEncoding.Strict().DecodeString(*entry.Payload)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "payload must be valid standard base64"})
			return
		}
		batch = append(batch, &payloadInfo{Payload: data, Time: entry.Time, TimeEnd: entry.TimeEnd, Type: entry.Type})
	}
	processBatch(c, batch, "json")
}
