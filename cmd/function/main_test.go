package main

import (
	"bytes"
	"compress/gzip"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/aws/aws-lambda-go/events"
	ginadapter "github.com/awslabs/aws-lambda-go-api-proxy/gin"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestJSON(t *testing.T) {
	for _, tc := range []struct {
		body   string
		status int
	}{
		{`{"data":"aGVsbG8="}`, http.StatusOK},
		{`{"data":"!!!"}`, http.StatusBadRequest},
		{`{}`, http.StatusBadRequest},
	} {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/api/json", bytes.NewBufferString(tc.body))
		req.Header.Set("Content-Type", "application/json")
		newRouter().ServeHTTP(w, req)
		if w.Code != tc.status {
			t.Fatalf("%s: status %d, body %s", tc.body, w.Code, w.Body)
		}
	}
}

func TestBinaryAPIGatewayEvent(t *testing.T) {
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", "test.bin")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = part.Write([]byte{0, 255, 42}); err != nil {
		t.Fatal(err)
	}
	if err = writer.Close(); err != nil {
		t.Fatal(err)
	}
	event := events.APIGatewayV2HTTPRequest{
		Version: "2.0", RawPath: "/api/bin", Headers: map[string]string{"content-type": writer.FormDataContentType()},
		Body: base64.StdEncoding.EncodeToString(body.Bytes()), IsBase64Encoded: true,
		RequestContext: events.APIGatewayV2HTTPRequestContext{HTTP: events.APIGatewayV2HTTPRequestContextHTTPDescription{Method: http.MethodPost, Path: "/api/bin"}},
	}
	response, err := ginadapter.NewV2(newRouter()).Proxy(event)
	if err != nil {
		t.Fatal(err)
	}
	var result struct {
		Size int `json:"size"`
	}
	if err := json.Unmarshal([]byte(response.Body), &result); err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK || result.Size != 3 {
		t.Fatalf("unexpected response: %+v", response)
	}
}

func TestMultipartPayloadWithoutFilename(t *testing.T) {
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	if err := writer.WriteField("payload", "aGVsbG8="); err != nil {
		t.Fatal(err)
	}
	if err := writer.WriteField("type", "meetings"); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/bin", &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	w := httptest.NewRecorder()
	newRouter().ServeHTTP(w, req)
	var result struct {
		Payload struct {
			Type string `json:"type"`
		} `json:"payload"`
		Metrics struct {
			DataBytes int `json:"data_bytes"`
		} `json:"metrics"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if w.Code != http.StatusOK || result.Payload.Type != "meetings" || result.Metrics.DataBytes != 8 {
		t.Fatalf("unexpected response: %d %s", w.Code, w.Body)
	}
}

func TestProtoEndpoints(t *testing.T) {
	message := []byte{0x0a, 0x03, 0x00, 0xff, 0x2a}
	var compressed bytes.Buffer
	gz := gzip.NewWriter(&compressed)
	if _, err := gz.Write(message); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		path     string
		body     []byte
		encoding string
		status   int
		size     int
	}{
		{"/api/proto", message, "", http.StatusOK, 3},
		{"/api/proto", compressed.Bytes(), "gzip", http.StatusOK, 3},
		{"/api/protozip", compressed.Bytes(), "gzip", http.StatusOK, 3},
		{"/api/protozip", message, "", http.StatusOK, 3},
		{"/api/proto", []byte{0x0a, 0x05, 0x01}, "", http.StatusBadRequest, 0},
		{"/api/protozip", message, "gzip", http.StatusBadRequest, 0},
		{"/api/proto", message, "br", http.StatusUnsupportedMediaType, 0},
	} {
		t.Run(tc.path+fmt.Sprint(tc.status), func(t *testing.T) {
			w := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, tc.path, bytes.NewReader(tc.body))
			req.Header.Set("Content-Type", "application/protobuf")
			req.Header.Set("Content-Encoding", tc.encoding)
			newRouter().ServeHTTP(w, req)
			if w.Code != tc.status {
				t.Fatalf("status %d: %s", w.Code, w.Body)
			}
			if tc.status == http.StatusOK {
				var response struct {
					Size    int `json:"size"`
					Metrics struct {
						RequestBytes  int   `json:"request_bytes"`
						DataBytes     int   `json:"data_bytes"`
						ProtobufBytes int   `json:"protobuf_bytes"`
						ElapsedNS     int64 `json:"elapsed_ns"`
					} `json:"metrics"`
				}
				if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
					t.Fatal(err)
				}
				if response.Size != tc.size {
					t.Fatalf("size = %d, want %d", response.Size, tc.size)
				}
				if response.Metrics.RequestBytes != len(tc.body) || response.Metrics.DataBytes != tc.size || response.Metrics.ProtobufBytes != len(message) || response.Metrics.ElapsedNS <= 0 {
					t.Fatalf("unexpected metrics: %+v", response.Metrics)
				}
			}
		})
	}
}

func TestProtoPayloadFields(t *testing.T) {
	var message []byte
	message = protowire.AppendTag(message, 1, protowire.BytesType)
	message = protowire.AppendBytes(message, []byte("hello"))
	for i, seconds := range []int64{1700000000, 1700000060} {
		timestamp, err := proto.Marshal(timestamppb.New(time.Unix(seconds, 0)))
		if err != nil {
			t.Fatal(err)
		}
		message = protowire.AppendTag(message, protowire.Number(i+2), protowire.BytesType)
		message = protowire.AppendBytes(message, timestamp)
	}
	message = protowire.AppendTag(message, 4, protowire.BytesType)
	message = protowire.AppendString(message, "example")
	w := httptest.NewRecorder()
	newRouter().ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/proto", bytes.NewReader(message)))
	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body)
	}
	var response struct {
		Size    int `json:"size"`
		Payload struct {
			Time    time.Time `json:"time"`
			TimeEnd time.Time `json:"time_end"`
			Type    string    `json:"type"`
		} `json:"payload"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Size != 5 || response.Payload.Time.Unix() != 1700000000 || response.Payload.TimeEnd.Unix() != 1700000060 || response.Payload.Type != "example" {
		t.Fatalf("unexpected response: %s", w.Body)
	}
}

func TestMultipartPayloadFields(t *testing.T) {
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	for name, value := range map[string]string{"type": "example", "time": "2024-01-01T00:00:00Z", "timeEnd": "2024-01-01T00:01:00Z"} {
		if err := writer.WriteField(name, value); err != nil {
			t.Fatal(err)
		}
	}
	part, err := writer.CreateFormFile("data", "input.bin")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write([]byte("hello")); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/bin", &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	w := httptest.NewRecorder()
	newRouter().ServeHTTP(w, req)
	var result struct {
		Size    int `json:"size"`
		Payload struct {
			Type    string    `json:"type"`
			Time    time.Time `json:"time"`
			TimeEnd time.Time `json:"time_end"`
		} `json:"payload"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if w.Code != http.StatusOK || result.Size != 5 || result.Payload.Type != "example" || result.Payload.Time.IsZero() || result.Payload.TimeEnd.Sub(result.Payload.Time) != time.Minute {
		t.Fatalf("unexpected response %d: %s", w.Code, w.Body)
	}
}

func TestJSONPayloadMetadata(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/api/json", bytes.NewBufferString(`{"data":"aGVsbG8=","time":"2024-01-01T00:00:00Z","time_end":"2024-01-01T00:01:00Z","type":"example"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	newRouter().ServeHTTP(w, req)
	var result struct {
		Size    int `json:"size"`
		Payload struct {
			Type    string    `json:"type"`
			Time    time.Time `json:"time"`
			TimeEnd time.Time `json:"time_end"`
		} `json:"payload"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if w.Code != http.StatusOK || result.Size != 5 || result.Payload.Type != "example" || result.Payload.Time.IsZero() || result.Payload.TimeEnd.Sub(result.Payload.Time) != time.Minute {
		t.Fatalf("unexpected response %d: %s", w.Code, w.Body)
	}
}

func TestJSONZip(t *testing.T) {
	var body bytes.Buffer
	writer := gzip.NewWriter(&body)
	if _, err := writer.Write([]byte(`{"data":"aGVsbG8=","type":"example"}`)); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/jsonzip", &body)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Content-Encoding", "gzip")
	w := httptest.NewRecorder()
	newRouter().ServeHTTP(w, req)
	var response struct {
		Size    int `json:"size"`
		Payload struct {
			Type string `json:"type"`
		} `json:"payload"`
		Metrics struct {
			Format string `json:"format"`
		} `json:"metrics"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if w.Code != http.StatusOK || response.Size != 5 || response.Payload.Type != "example" || response.Metrics.Format != "json" {
		t.Fatalf("unexpected response: %d %s", w.Code, w.Body)
	}
}

func TestMultipartGzip(t *testing.T) {
	var form bytes.Buffer
	multipartWriter := multipart.NewWriter(&form)
	part, err := multipartWriter.CreateFormFile("file", "data.bin")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write([]byte("hello")); err != nil {
		t.Fatal(err)
	}
	if err := multipartWriter.WriteField("type", "example"); err != nil {
		t.Fatal(err)
	}
	if err := multipartWriter.Close(); err != nil {
		t.Fatal(err)
	}
	var compressed bytes.Buffer
	gz := gzip.NewWriter(&compressed)
	if _, err := gz.Write(form.Bytes()); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/binzip", &compressed)
	req.Header.Set("Content-Type", multipartWriter.FormDataContentType())
	req.Header.Set("Content-Encoding", "gzip")
	w := httptest.NewRecorder()
	newRouter().ServeHTTP(w, req)
	var result struct {
		Size    int `json:"size"`
		Payload struct {
			Type string `json:"type"`
		} `json:"payload"`
		Metrics struct {
			Format string `json:"format"`
		} `json:"metrics"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if w.Code != http.StatusOK || result.Size != 5 || result.Payload.Type != "example" || result.Metrics.Format != "multipart/form-data" {
		t.Fatalf("unexpected response %d: %s", w.Code, w.Body)
	}
}
