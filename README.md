# Lambda protocol comparison

A Go and Gin application for comparing JSON, multipart, and Protocol Buffers requests, with and without gzip compression. It runs locally or on AWS Lambda behind a public Function URL. AWS CDK infrastructure is written in Go.

Each request returns the decoded payload metadata and measurements such as request body size and server-side processing time. The application is a starting point for experiments; `process` in [`cmd/function/main.go`](cmd/function/main.go) is where application-specific processing can be added.

## Requirements

- Go 1.23 or later and Make.
- For the request script: `curl`, `jq`, `protoc`, and `gzip`. On macOS, install `protoc` with `brew install protobuf`.
- For AWS deployment: an AWS account, AWS CLI, Node.js, CDK CLI v2, and credentials with permission to deploy the stack.

## Run locally

Start the server in one terminal:

```sh
make local
```

In another terminal, send the example requests:

```sh
make test-local
```

The server listens on `http://127.0.0.1:8080`. Restart it after changing Go code. The script requires no AWS credentials in local mode.

To run Go tests separately:

```sh
go test ./...
```

## Deploy to AWS

Configure AWS credentials, then create a `.env` file in the project root. Make reads this file and exports its values to its commands. Use unquoted `KEY=value` entries:

```dotenv
AWS_ACCOUNT_ID=123456789012
AWS_REGION=us-east-1
```

Replace the account, region, and profile with your own. `AWS_ACCOUNT_ID` and `AWS_REGION` are required by the CDK application. `.env` is ignored by Git.


If this AWS account and region have not yet been bootstrapped for CDK, run the following once, substituting your account, region, and profile:

```sh
cdk bootstrap aws://123456789012/us-east-1 --profile my-profile
```

Build and deploy:

```sh
go mod download
make synth
make deploy
```

`make deploy` builds the Lambda binary for Linux ARM64 before running CDK. The stack is named `BinaryLambdaStack`; its `ApiUrl` output is the Function URL. Redeploy after changing the function. The URL is public and has no authentication; configure access controls before using private data. AWS resources may incur charges.

To run the request script against the deployed stack:

```sh
make test
```

The script looks up `ApiUrl` in the CloudFormation outputs for `BinaryLambdaStack` using the active AWS credentials and region. To delete the stack, run `make destroy`; CDK bootstrap resources remain.

## Endpoints

All endpoints use `POST`. The gzip variants in the test script compress the entire HTTP body and send `Content-Encoding: gzip`. For multipart requests, the boundary and all form fields are part of the compressed body.

| Endpoint | Request body | Content-Type |
| --- | --- | --- |
| `/api/json` | JSON with base64-encoded `payload` | `application/json` |
| `/api/jsonzip` | gzip-compressed JSON | `application/json` |
| `/api/jsonbatch` | JSON object with an `entries` array | `application/json` |
| `/api/jsonbatchzip` | gzip-compressed JSON batch | `application/json` |
| `/api/bin` | multipart with binary file | `multipart/form-data; boundary=...` |
| `/api/binzip` | gzip-compressed multipart body | `multipart/form-data; boundary=...` |
| `/api/binbatch` | multipart with indexed batch fields | `multipart/form-data; boundary=...` |
| `/api/binbatchzip` | gzip-compressed multipart batch | `multipart/form-data; boundary=...` |
| `/api/proto` | serialized Protobuf `Payload` | `application/protobuf` |
| `/api/protozip` | gzip-compressed Protobuf `Payload` | `application/protobuf` |
| `/api/protobatch` | serialized Protobuf `Batch` | `application/protobuf` |
| `/api/protobatchzip` | gzip-compressed Protobuf `Batch` | `application/protobuf` |
| `/api/health` | no request body required | — |

JSON requests accept `payload`, `time`, `time_end`, and `type`. `payload` is required and uses standard base64; timestamps use RFC 3339. JSON batch requests wrap these objects in an `entries` array, for example `{"entries":[{"type":"example","payload":"aGVsbG8="}]}`. An empty array is allowed; any invalid entry rejects the whole batch. Multipart accepts a file under `file` or `payload`, plus optional text fields `time`, `timeEnd` or `time_end`, and `type`. Protobuf uses `Payload` or `Batch` from [`proto/input.proto`](proto/input.proto).

Multipart batch uses field names such as `entries[0][type]`, `entries[0][time]`, `entries[0][timeEnd]`, and `entries[0][payload]`, followed by `entries[1][...]` and so on. Indices must be contiguous from zero; each entry requires `payload`. Fields may arrive in any order. Payloads are raw part bytes, whether text or a file, and are not base64-decoded. Invalid fields or timestamps reject the entire batch.

For example, with `API_URL` set to the Function URL or local server:

```sh
curl -X POST "${API_URL%/}/api/json" \
  -H 'Content-Type: application/json' \
  -d '{"payload":"aGVsbG8=","time":"2024-01-01T00:00:00Z","time_end":"2024-01-01T00:01:00Z","type":"example"}'

curl -X POST "${API_URL%/}/api/bin" \
  -F 'file=@./example.bin' -F 'type=example'
```

See [`test.sh`](test.sh) for gzip and Protobuf request construction. The request body is limited to 20 MiB. For JSON batch, Protobuf, and multipart gzip requests, the decompressed body is also limited to 20 MiB. AWS Lambda synchronous payload limits and base64 overhead can impose lower practical limits.

## Interpreting results

Responses include a `payload` object, or a `batch` array for batch requests, containing the parsed metadata and a `metrics` object. The binary data itself is not returned.

| Metric | Meaning |
| --- | --- |
| `request_bytes` | Size of the HTTP request body received by Gin, including compression when used |
| `data_bytes` | Size of the decoded binary data |
| `batch_entries`, `total_data_bytes` | Number of batch entries and total decoded binary data size |
| `elapsed_ns` | Nanoseconds from the start of Gin middleware to just before the response is written |
| `format`, `endpoint` | The parsed format and request path |
| `content_type`, `content_encoding` | The received headers (`identity` is reported when no encoding is supplied) |
| `data_encoding` | `base64` for JSON |
| `protobuf_bytes` | Size of the Protobuf message after decompression, when applicable |
| `filename`, `file_content_type` | Multipart file metadata provided by the client |

`elapsed_ns` excludes network time, Lambda cold starts, adapter work, and response serialization. For useful comparisons, use identical payloads, run multiple iterations, measure end-to-end latency on the client, and consider both compressible and random data.
