package handlers

import (
	"bytes"
	"compress/gzip"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

func BinHandler(c *gin.Context) {
	reader, ok := multipartReader(c)
	if !ok {
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

func multipartReader(c *gin.Context) (*multipart.Reader, bool) {
	encoding := strings.TrimSpace(c.GetHeader("Content-Encoding"))

	if strings.EqualFold(encoding, "gzip") {
		gz, err := gzip.NewReader(c.Request.Body)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "expected gzip-compressed multipart body"})
			return nil, false
		}
		defer gz.Close()

		body, err := io.ReadAll(io.LimitReader(gz, MaxBodySize+1))
		if err != nil || len(body) > MaxBodySize {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid or oversized multipart body"})
			return nil, false
		}

		c.Request.Body = io.NopCloser(bytes.NewReader(body))

	} else if encoding != "" && !strings.EqualFold(encoding, "identity") {
		c.JSON(http.StatusUnsupportedMediaType, gin.H{"error": "unsupported content encoding"})
		return nil, false
	}

	reader, err := c.Request.MultipartReader()
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "expected multipart/form-data"})
		return nil, false
	}
	return reader, true
}

func BinBatchHandler(c *gin.Context) {
	reader, ok := multipartReader(c)
	if !ok {
		return
	}
	type entry struct {
		payload payloadInfo
		seen    map[string]bool
	}
	entries := make(map[int]*entry)
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
		name, ok := strings.CutPrefix(part.FormName(), "entries[")
		indexText, field, found := strings.Cut(name, "][")
		field, suffix := strings.CutSuffix(field, "]")
		index, indexErr := strconv.Atoi(indexText)
		if !ok || !found || !suffix || indexErr != nil || index < 0 || strconv.Itoa(index) != indexText {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid batch field name"})
			return
		}
		if field == "time_end" {
			field = "timeEnd"
		}
		if field != "type" && field != "time" && field != "timeEnd" && field != "payload" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "unknown batch field"})
			return
		}
		item := entries[index]
		if item == nil {
			item = &entry{seen: make(map[string]bool)}
			entries[index] = item
		}
		if item.seen[field] {
			c.JSON(http.StatusBadRequest, gin.H{"error": "duplicate batch field"})
			return
		}
		item.seen[field] = true
		switch field {
		case "payload":
			item.payload.Payload = value
		case "type":
			item.payload.Type = string(value)
		case "time", "timeEnd":
			t, err := time.Parse(time.RFC3339Nano, string(value))
			if err != nil {
				c.JSON(http.StatusBadRequest, gin.H{"error": "invalid time format"})
				return
			}
			if field == "time" {
				item.payload.Time = &t
			} else {
				item.payload.TimeEnd = &t
			}
		}
	}
	if len(entries) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "multipart batch must contain entries"})
		return
	}
	batch := make([]*payloadInfo, 0, len(entries))
	for i := 0; i < len(entries); i++ {
		item, ok := entries[i]
		if !ok || !item.seen["payload"] {
			c.JSON(http.StatusBadRequest, gin.H{"error": "batch entries must be contiguous and contain a payload"})
			return
		}
		batch = append(batch, &item.payload)
	}
	processBatch(c, batch, "multipart/form-data")
}
