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
	"github.com/emp1re/protocol-comparision/cmd/function/handlers"
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

func TestJSONBatchEndpoints(t *testing.T) {
	body := `{"entries":[{"type":"first","time":"2024-01-01T00:00:00Z","time_end":"2024-01-01T00:01:00Z","payload":"aGVsbG8="},{"type":"second","payload":"AAE="}]}`
	compress := func(input string) []byte {
		t.Helper()
		var buf bytes.Buffer
		writer := gzip.NewWriter(&buf)
		if _, err := writer.Write([]byte(input)); err != nil {
			t.Fatal(err)
		}
		if err := writer.Close(); err != nil {
			t.Fatal(err)
		}
		return buf.Bytes()
	}
	for _, tc := range []struct {
		name, path, encoding string
		body                 []byte
		status               int
	}{
		{"plain", "/api/jsonbatch", "", []byte(body), http.StatusOK},
		{"gzip", "/api/jsonbatchzip", "gzip", compress(body), http.StatusOK},
		{"empty", "/api/jsonbatch", "", []byte(`{"entries":[]}`), http.StatusOK},
		{"missing entries", "/api/jsonbatch", "", []byte(`{}`), http.StatusBadRequest},
		{"null entry", "/api/jsonbatch", "", []byte(`{"entries":[null]}`), http.StatusBadRequest},
		{"missing payload", "/api/jsonbatch", "", []byte(`{"entries":[{}]}`), http.StatusBadRequest},
		{"invalid base64", "/api/jsonbatch", "", []byte(`{"entries":[{"payload":"!!!"}]}`), http.StatusBadRequest},
		{"invalid timestamp", "/api/jsonbatch", "", []byte(`{"entries":[{"payload":"","time":"bad"}]}`), http.StatusBadRequest},
		{"invalid gzip", "/api/jsonbatchzip", "gzip", []byte(body), http.StatusBadRequest},
		{"truncated gzip", "/api/jsonbatchzip", "gzip", compress(body)[:20], http.StatusBadRequest},
		{"unsupported encoding", "/api/jsonbatch", "br", []byte(body), http.StatusUnsupportedMediaType},
		{"oversized decompressed", "/api/jsonbatchzip", "gzip", compress(string(bytes.Repeat([]byte(" "), handlers.MaxBodySize+1))), http.StatusBadRequest},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, tc.path, bytes.NewReader(tc.body))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Content-Encoding", tc.encoding)
			w := httptest.NewRecorder()
			newRouter().ServeHTTP(w, req)
			if w.Code != tc.status {
				t.Fatalf("status %d, want %d: %s", w.Code, tc.status, w.Body)
			}
			if tc.status != http.StatusOK {
				return
			}
			var response struct {
				Batch []struct {
					Type    string     `json:"type"`
					Time    *time.Time `json:"time"`
					TimeEnd *time.Time `json:"time_end"`
				} `json:"batch"`
				Metrics struct {
					BatchEntries   int    `json:"batch_entries"`
					TotalDataBytes int    `json:"total_data_bytes"`
					RequestBytes   int    `json:"request_bytes"`
					Format         string `json:"format"`
					DataEncoding   string `json:"data_encoding"`
				} `json:"metrics"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
				t.Fatal(err)
			}
			wantEntries, wantBytes := 2, 7
			if tc.name == "empty" {
				wantEntries, wantBytes = 0, 0
			}
			if response.Batch == nil || len(response.Batch) != wantEntries ||
				response.Metrics.BatchEntries != wantEntries || response.Metrics.TotalDataBytes != wantBytes ||
				response.Metrics.RequestBytes != len(tc.body) || response.Metrics.Format != "json" ||
				response.Metrics.DataEncoding != "base64" {
				t.Fatalf("unexpected response: %s", w.Body)
			}
			if wantEntries > 0 && (response.Batch[0].Type != "first" || response.Batch[1].Type != "second" ||
				response.Batch[0].Time == nil || response.Batch[0].TimeEnd == nil ||
				response.Batch[0].TimeEnd.Sub(*response.Batch[0].Time) != time.Minute) {
				t.Fatalf("unexpected batch: %s", w.Body)
			}
		})
	}
}

func TestProtoBatchTotalDataBytes(t *testing.T) {
	var body []byte
	for _, data := range []string{"hello", "ok"} {
		entry := protowire.AppendTag(nil, 4, protowire.BytesType)
		entry = protowire.AppendString(entry, data)
		body = protowire.AppendTag(body, 1, protowire.BytesType)
		body = protowire.AppendBytes(body, entry)
	}
	w := httptest.NewRecorder()
	newRouter().ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/protobatch", bytes.NewReader(body)))
	var response struct {
		Metrics struct {
			BatchEntries   int `json:"batch_entries"`
			TotalDataBytes int `json:"total_data_bytes"`
		} `json:"metrics"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if w.Code != http.StatusOK || response.Metrics.BatchEntries != 2 || response.Metrics.TotalDataBytes != 7 {
		t.Fatalf("unexpected response: %d %s", w.Code, w.Body)
	}
}

func TestMultipartBatchEndpoints(t *testing.T) {
	type field struct{ name, value string }
	makeBody := func(fields []field) ([]byte, string) {
		t.Helper()
		var body bytes.Buffer
		writer := multipart.NewWriter(&body)
		for _, f := range fields {
			if err := writer.WriteField(f.name, f.value); err != nil {
				t.Fatal(err)
			}
		}
		if err := writer.Close(); err != nil {
			t.Fatal(err)
		}
		return body.Bytes(), writer.FormDataContentType()
	}
	fields := []field{
		{"entries[1][payload]", "AAE="},
		{"entries[0][type]", "meetings"},
		{"entries[0][time]", "2026-11-26T08:18:00-05:00"},
		{"entries[0][timeEnd]", "2026-11-26T08:19:00-05:00"},
		{"entries[0][payload]", "aGVsbG8="},
		{"entries[1][type]", "second"},
		{"entries[2][payload]", "\x00\xff"},
	}
	body, contentType := makeBody(fields)
	var compressed bytes.Buffer
	gz := gzip.NewWriter(&compressed)
	if _, err := gz.Write(body); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	var oversized bytes.Buffer
	oversizedGzip := gzip.NewWriter(&oversized)
	if _, err := oversizedGzip.Write(bytes.Repeat([]byte("x"), handlers.MaxBodySize+1)); err != nil {
		t.Fatal(err)
	}
	if err := oversizedGzip.Close(); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, path, encoding string
		body                 []byte
		status               int
	}{
		{"plain", "/api/binbatch", "", body, http.StatusOK},
		{"gzip", "/api/binbatchzip", "gzip", compressed.Bytes(), http.StatusOK},
		{"invalid gzip", "/api/binbatchzip", "gzip", body, http.StatusBadRequest},
		{"truncated gzip", "/api/binbatchzip", "gzip", compressed.Bytes()[:20], http.StatusBadRequest},
		{"unsupported encoding", "/api/binbatch", "br", body, http.StatusUnsupportedMediaType},
		{"oversized gzip", "/api/binbatchzip", "gzip", oversized.Bytes(), http.StatusBadRequest},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, tc.path, bytes.NewReader(tc.body))
			req.Header.Set("Content-Type", contentType)
			req.Header.Set("Content-Encoding", tc.encoding)
			w := httptest.NewRecorder()
			newRouter().ServeHTTP(w, req)
			if w.Code != tc.status {
				t.Fatalf("status %d, want %d: %s", w.Code, tc.status, w.Body)
			}
			if tc.status != http.StatusOK {
				return
			}
			var response struct {
				Batch []struct {
					Type    string     `json:"type"`
					Time    *time.Time `json:"time"`
					TimeEnd *time.Time `json:"time_end"`
				} `json:"batch"`
				Metrics struct {
					BatchEntries   int    `json:"batch_entries"`
					TotalDataBytes int    `json:"total_data_bytes"`
					RequestBytes   int    `json:"request_bytes"`
					Format         string `json:"format"`
				} `json:"metrics"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
				t.Fatal(err)
			}
			if len(response.Batch) != 3 || response.Batch[0].Type != "meetings" || response.Batch[1].Type != "second" ||
				response.Batch[0].Time == nil || response.Batch[0].TimeEnd == nil ||
				response.Batch[0].TimeEnd.Sub(*response.Batch[0].Time) != time.Minute ||
				response.Metrics.BatchEntries != 3 || response.Metrics.TotalDataBytes != 14 ||
				response.Metrics.RequestBytes != len(tc.body) || response.Metrics.Format != "multipart/form-data" {
				t.Fatalf("unexpected response: %s", w.Body)
			}
		})
	}
	for _, tc := range []struct {
		name   string
		fields []field
	}{
		{"empty", nil},
		{"missing payload", []field{{"entries[0][type]", "meetings"}}},
		{"sparse indices", []field{{"entries[0][payload]", "a"}, {"entries[2][payload]", "b"}}},
		{"invalid index", []field{{"entries[-1][payload]", "a"}}},
		{"invalid name", []field{{"payload", "a"}}},
		{"duplicate", []field{{"entries[0][payload]", "a"}, {"entries[0][payload]", "b"}}},
		{"invalid time", []field{{"entries[0][payload]", "a"}, {"entries[0][time]", "invalid"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body, contentType := makeBody(tc.fields)
			req := httptest.NewRequest(http.MethodPost, "/api/binbatch", bytes.NewReader(body))
			req.Header.Set("Content-Type", contentType)
			w := httptest.NewRecorder()
			newRouter().ServeHTTP(w, req)
			if w.Code != http.StatusBadRequest {
				t.Fatalf("status %d, want 400: %s", w.Code, w.Body)
			}
		})
	}
}

func TestMultipartBatchFileAndSingleGzip(t *testing.T) {
	for _, batch := range []bool{false, true} {
		t.Run(fmt.Sprint("batch=", batch), func(t *testing.T) {
			var body bytes.Buffer
			writer := multipart.NewWriter(&body)
			field, path := "file", "/api/binzip"
			if batch {
				field, path = "entries[0][payload]", "/api/binbatchzip"
			}
			part, err := writer.CreateFormFile(field, "data.bin")
			if err != nil {
				t.Fatal(err)
			}
			if _, err := part.Write([]byte{0, 255, 42}); err != nil {
				t.Fatal(err)
			}
			if err := writer.Close(); err != nil {
				t.Fatal(err)
			}
			var compressed bytes.Buffer
			gz := gzip.NewWriter(&compressed)
			if _, err := gz.Write(body.Bytes()); err != nil {
				t.Fatal(err)
			}
			if err := gz.Close(); err != nil {
				t.Fatal(err)
			}
			req := httptest.NewRequest(http.MethodPost, path, &compressed)
			req.Header.Set("Content-Type", writer.FormDataContentType())
			req.Header.Set("Content-Encoding", "gzip")
			w := httptest.NewRecorder()
			newRouter().ServeHTTP(w, req)
			var result struct {
				Metrics struct {
					DataBytes      int `json:"data_bytes"`
					TotalDataBytes int `json:"total_data_bytes"`
				} `json:"metrics"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
				t.Fatal(err)
			}
			if w.Code != http.StatusOK || (!batch && result.Metrics.DataBytes != 3) ||
				(batch && result.Metrics.TotalDataBytes != 3) {
				t.Fatalf("unexpected response: %d %s", w.Code, w.Body)
			}
		})
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
