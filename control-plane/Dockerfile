FROM golang:1.24-alpine AS builder

WORKDIR /app

RUN apk add --no-cache protobuf-dev protobuf git

RUN go install google.golang.org/protobuf/cmd/protoc-gen-go@latest
RUN go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest

RUN mkdir -p proto
COPY proto/controlplane.proto proto/

RUN protoc --go_out=. --go_opt=paths=source_relative \
    --go-grpc_out=. --go-grpc_opt=paths=source_relative \
    proto/controlplane.proto

COPY go.mod go.sum ./
RUN go mod download

COPY *.go ./

RUN CGO_ENABLED=0 GOOS=linux go build -o controlplane main.go

FROM alpine:latest

RUN apk --no-cache add ca-certificates

WORKDIR /root/

COPY --from=builder /app/controlplane .

EXPOSE 50051

CMD ["./controlplane"]
