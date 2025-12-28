# Use the latest Go image (which will cover 1.24 functionality)
FROM golang:alpine

# Set working directory
WORKDIR /app

# Install git and other dependencies (needed for fetching packages)
RUN apk add --no-cache git

# Copy the source code
COPY . .

# Initialize Go module inside the container (Dynamic)
# It creates go.mod and go.sum, downloads dependencies, and builds.
RUN go mod init myproject && \
    go get -u github.com/gin-gonic/gin && \
    go get -u golang.org/x/net/html && \
    go get -u github.com/nyaruka/phonenumbers && \
    go mod tidy && \
    go build -o main .

# Expose the port
EXPOSE 8080

# Run the binary
CMD ["./main"]
