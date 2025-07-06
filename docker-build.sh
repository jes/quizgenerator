#!/bin/bash

# Build the quiz generator webserver Docker image
echo "Building quiz-generator webserver Docker image..."

# Build the image
docker build -t quiz-generator:latest .

if [ $? -eq 0 ]; then
    echo "✅ Docker image built successfully!"
    echo "Image name: quiz-generator:latest"
else
    echo "❌ Docker build failed!"
    exit 1
fi 