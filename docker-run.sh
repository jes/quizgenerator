#!/bin/bash

# Run the quiz generator webserver Docker container
echo "Starting quiz-generator webserver..."

# Get the current directory (should be ~/quizgenerator)
CURRENT_DIR=$(pwd)

# Check if the image exists
if ! docker image inspect quiz-generator:latest >/dev/null 2>&1; then
    echo "❌ Docker image 'quiz-generator:latest' not found!"
    echo "Please run './docker-build.sh' first to build the image."
    exit 1
fi

# Check if OPENAI_API_KEY is set, if not try to load from ./key file
if [ -z "$OPENAI_API_KEY" ]; then
    if [ -f "./key" ]; then
        echo "📁 Loading OpenAI API key from ./key file..."
        export OPENAI_API_KEY=$(cat ./key | tr -d '\n\r')
        echo "✅ API key loaded from ./key file"
    else
        echo "❌ OPENAI_API_KEY environment variable is not set and ./key file not found!"
        echo "Please either:"
        echo "1. Set the environment variable: export OPENAI_API_KEY='your-api-key-here'"
        echo "2. Create a ./key file with your API key"
        exit 1
    fi
fi

# Create directories if they don't exist
mkdir -p "$CURRENT_DIR/data"
mkdir -p "$CURRENT_DIR/log"

# Run the container
docker run -d \
    --name quiz-generator \
    --restart unless-stopped \
    -p 8180:8180 \
    -e OPENAI_API_KEY="$OPENAI_API_KEY" \
    -v "$CURRENT_DIR/data:/app/data" \
    -v "$CURRENT_DIR/log:/app/log" \
    quiz-generator:latest

if [ $? -eq 0 ]; then
    echo "✅ Quiz generator webserver started successfully!"
    echo "🌐 Web interface available at: http://localhost:8180"
    echo "📁 Database stored in: $CURRENT_DIR/data/"
    echo "📝 Logs stored in: $CURRENT_DIR/log/"
    echo ""
    echo "To stop the server: docker stop quiz-generator"
    echo "To view logs: docker logs quiz-generator"
else
    echo "❌ Failed to start the container!"
    exit 1
fi 