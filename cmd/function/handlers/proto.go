package handlers

import (
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func ProtoHandler(c *gin.Context) {
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

	body, err := io.ReadAll(io.LimitReader(reader, MaxBodySize+1))
	if err != nil || len(body) > MaxBodySize {
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

func ProtoBatchHandler(c *gin.Context) {
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

	body, err := io.ReadAll(io.LimitReader(reader, MaxBodySize+1))
	if err != nil || len(body) > MaxBodySize {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid or oversized protobuf body"})
		return
	}

	c.Set("protobuf_bytes", len(body))

	batch, err := decodeProtoBatch(body)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid protobuf batch"})
		return
	}

	processBatch(c, batch, "protobuf")
}

// Decode Payload from proto/input.proto, ignoring unknown fields.
func decodeProto(body []byte) (*payloadInfo, error) {
	payload := &payloadInfo{}
	for len(body) > 0 {
		num, typ, n := protowire.ConsumeTag(body)
		if n < 0 {
			return nil, protowire.ParseError(n)
		}
		fmt.Printf("Tag: num=%d typ=%v\n", num, typ)
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
