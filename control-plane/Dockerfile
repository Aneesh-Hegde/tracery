FROM golang:1.23-alpine AS builder

WORKDIR /app

RUN apk add --no-cache protobuf-dev protobuf git

RUN go install google.golang.org/protobuf/cmd/protoc-gen-go@latest
RUN go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest

COPY proto/controlplane.proto proto/
RUN mkdir -p pb

RUN protoc --go_out=pb --go_opt=paths=source_relative \
    --go-grpc_out=pb --go-grpc_opt=paths=source_relative \
    proto/controlplane.proto

COPY go.mod go.sum ./
RUN go mod download

COPY *.go ./

RUN CGO_ENABLED=0 GOOS=linux go build -o controlplane .

FROM alpine:latest

RUN apk --no-cache add ca-certificates

WORKDIR /root/

COPY --from=builder /app/controlplane .

EXPOSE 50051

CMD ["./controlplane"]
