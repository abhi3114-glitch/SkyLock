# Script to start a 3-node SkyLock cluster
# Run this from the SkyLock project root directory

Write-Host "Starting SkyLock 3-node cluster..." -ForegroundColor Cyan

# Build the project first
Write-Host "Building SkyLock..." -ForegroundColor Yellow
go build -o skylock.exe ./cmd/skylock
if ($LASTEXITCODE -ne 0) {
    Write-Host "Build failed!" -ForegroundColor Red
    exit 1
}

Write-Host "Build successful!" -ForegroundColor Green

# Create data directories
New-Item -ItemType Directory -Force -Path "data/node1" | Out-Null
New-Item -ItemType Directory -Force -Path "data/node2" | Out-Null
New-Item -ItemType Directory -Force -Path "data/node3" | Out-Null

# Start nodes in separate windows
Write-Host "Starting Node 1 (gRPC: 5001, HTTP: 8081)..." -ForegroundColor Yellow
Start-Process -FilePath ".\skylock.exe" -ArgumentList "--config", "configs/node1.yaml" -WindowStyle Normal

Start-Sleep -Seconds 1

Write-Host "Starting Node 2 (gRPC: 5002, HTTP: 8082)..." -ForegroundColor Yellow
Start-Process -FilePath ".\skylock.exe" -ArgumentList "--config", "configs/node2.yaml" -WindowStyle Normal

Start-Sleep -Seconds 1

Write-Host "Starting Node 3 (gRPC: 5003, HTTP: 8083)..." -ForegroundColor Yellow
Start-Process -FilePath ".\skylock.exe" -ArgumentList "--config", "configs/node3.yaml" -WindowStyle Normal

Write-Host ""
Write-Host "SkyLock cluster started!" -ForegroundColor Green
Write-Host ""
Write-Host "Available endpoints:" -ForegroundColor Cyan
Write-Host "  Node 1: http://localhost:8081"
Write-Host "  Node 2: http://localhost:8082"
Write-Host "  Node 3: http://localhost:8083"
Write-Host ""
Write-Host "To check cluster status:" -ForegroundColor Cyan
Write-Host "  curl http://localhost:8081/v1/cluster/status"
Write-Host ""
Write-Host "To get current leader:" -ForegroundColor Cyan
Write-Host "  curl http://localhost:8081/v1/cluster/leader"
