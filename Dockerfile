# Use the official Python image from the Docker Hub
FROM python:3.12-alpine

# Set the working directory inside the container
WORKDIR /app

# Copy the requirements file into the container
COPY requirements.txt .

# Install Python dependencies
RUN pip3 install --upgrade pip && pip3 install -r requirements.txt && apk add --no-cache fping

# Copy the rest of the application code
COPY ping-exporter.py .

# Expose the port the app runs on
EXPOSE 9107

# Run the application

CMD ["python","-u", "ping-exporter.py"]