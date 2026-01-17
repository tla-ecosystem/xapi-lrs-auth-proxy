# Windows Quick Start Guide

Complete step-by-step guide for building and running xAPI LRS Auth Proxy on Windows.

## Prerequisites

### Install Go

1. **Download Go:**
   - Visit: https://go.dev/dl/
   - Download: `go1.21.x.windows-amd64.msi` (or latest version)

2. **Run Installer:**
   - Double-click the `.msi` file
   - Accept all defaults
   - Click "Install"

3. **Add Go to PATH** (if not automatic):
   - Press **Windows key**, type `environment variables`
   - Click **"Edit the system environment variables"**
   - Click **"Environment Variables..."** button
   - In **"System variables"** section, find **"Path"**
   - Click **"Edit..."**
   - Click **"New"**
   - Add: `C:\Program Files\Go\bin`
   - Click **"OK"** on all windows

4. **Verify Installation:**
   - **Close and reopen** Command Prompt/PowerShell
   - Run: `go version`
   - Should show: `go version go1.21.x windows/amd64`

**If `go version` doesn't work after installation:**
- Make sure you **closed and reopened** your terminal
- Try **restarting your computer**
- Verify Go is in PATH (see step 3 above)

---

## Getting the Code

### Option 1: Clone from GitHub (Recommended)

```powershell
# Navigate to your projects folder
cd C:\Users\YourName\Documents
# Or: cd E:\projects

# Clone the repository
git clone https://github.com/tla-ecosystem/xapi-lrs-auth-proxy.git

# Navigate into the project
cd xapi-lrs-auth-proxy
```

✅ **This preserves the correct folder structure automatically**

### Option 2: Download ZIP from GitHub

1. Go to: https://github.com/tla-ecosystem/xapi-lrs-auth-proxy
2. Click green **"Code"** button → **"Download ZIP"**
3. Extract the ZIP file
4. Open PowerShell in the extracted folder

⚠️ **Important:** Make sure the folder structure is preserved after extraction:
```
xapi-lrs-auth-proxy\
├── cmd\
│   └── proxy\
│       └── main.go
├── internal\
│   ├── config\
│   ├── handlers\
│   ├── middleware\
│   ├── models\
│   ├── store\
│   └── validator\
└── go.mod
```

**If files are all in the root directory (not in folders), see "Troubleshooting" section below.**

---

## Building the Proxy

### Step 1: Navigate to Project Directory

```powershell
cd path\to\xapi-lrs-auth-proxy
```

### Step 2: Download Dependencies

```powershell
go mod download
go mod tidy
```

**What this does:**
- Downloads all required Go packages
- Updates `go.sum` with checksums
- Ensures dependencies are correct

**Expected output:**
```
go: downloading github.com/gorilla/mux v1.8.1
go: downloading github.com/golang-jwt/jwt/v5 v5.2.0
go: downloading github.com/lib/pq v1.10.9
...
```

### Step 3: Build the Executable

```powershell
go build -o xapi-proxy.exe ./cmd/proxy
```

**Expected output:**
```
(builds silently for 5-10 seconds)
```

### Step 4: Verify Build

```powershell
dir xapi-proxy.exe
```

**You should see:**
```
Mode                 LastWriteTime         Length Name
----                 -------------         ------ ----
-a----         1/17/2026   3:15 PM     15,234,567 xapi-proxy.exe
```

✅ **Build successful!**

---

## Configuration

### Create Configuration File

```powershell
# Copy example config
copy config.example.yaml config.yaml

# Edit with Notepad
notepad config.yaml
```

**Minimal configuration for testing:**

```yaml
mode: single-tenant

server:
  port: 8080

lrs:
  endpoint: "https://lrs.adlnet.gov/xapi/"
  username: "test"
  password: "test"

auth:
  jwt_secret: "your-secret-key-minimum-32-characters-long"
  jwt_ttl_seconds: 3600
  lms_api_keys:
    - "test-api-key-12345"
```

**For production:**
- Use environment variables for secrets
- Point to your actual LRS endpoint
- Use strong, random JWT secret (32+ characters)

---

## Running the Proxy

### Start the Proxy

```powershell
.\xapi-proxy.exe --config config.yaml
```

**Expected output:**
```json
{"level":"info","msg":"Starting xAPI LRS Auth Proxy","version":"1.0.0"}
{"level":"info","msg":"Initializing single-tenant mode"}
{"level":"info","msg":"Starting HTTP server","addr":":8080"}
```

✅ **The proxy is running on port 8080**

**Keep this window open!** The proxy must stay running.

### Stop the Proxy

Press **Ctrl+C** in the window where proxy is running.

---

## Testing

### Test 1: Health Check

Open a **NEW** PowerShell window:

```powershell
Invoke-RestMethod -Uri http://localhost:8080/health
```

**Expected output:**
```
status  version
------  -------
ok      1.0.0
```

### Test 2: Request JWT Token

```powershell
$body = @{
    actor = @{
        objectType = "Agent"
        mbox = "mailto:test@example.com"
        name = "Test Learner"
    }
    registration = "test-session-123"
    activity_id = "https://example.com/test-lesson"
    permissions = @{
        write = "actor-activity-registration-scoped"
        read = "actor-activity-registration-scoped"
    }
} | ConvertTo-Json

$response = Invoke-RestMethod -Uri http://localhost:8080/auth/token `
    -Method POST `
    -Headers @{Authorization="Bearer test-api-key-12345"} `
    -ContentType "application/json" `
    -Body $body

Write-Host "Token received!"
Write-Host "Expires at: $($response.expires_at)"
$token = $response.token
```

**Expected:**
```
Token received!
Expires at: 2026-01-17T16:30:00Z
```

### Test 3: Post xAPI Statement

```powershell
$statement = @(
    @{
        actor = @{
            objectType = "Agent"
            mbox = "mailto:test@example.com"
        }
        verb = @{
            id = "http://adlnet.gov/expapi/verbs/completed"
            display = @{"en-US" = "completed"}
        }
        object = @{
            id = "https://example.com/test-lesson"
            objectType = "Activity"
        }
        context = @{
            registration = "test-session-123"
        }
    }
) | ConvertTo-Json -Depth 10

Invoke-RestMethod -Uri http://localhost:8080/xapi/statements `
    -Method POST `
    -Headers @{
        Authorization="Bearer $token"
        "X-Experience-API-Version"="1.0.3"
    } `
    -ContentType "application/json" `
    -Body $statement

Write-Host "Statement posted successfully!"
```

---

## Convenience Scripts

### Create `build.bat`

```batch
@echo off
echo Building xAPI LRS Auth Proxy...
go mod tidy
go build -o xapi-proxy.exe ./cmd/proxy
if %ERRORLEVEL% EQU 0 (
    echo.
    echo Build successful!
    echo Run with: run.bat
) else (
    echo.
    echo Build failed!
)
pause
```

### Create `run.bat`

```batch
@echo off
echo Starting xAPI LRS Auth Proxy...
echo Press Ctrl+C to stop
echo.
xapi-proxy.exe --config config.yaml
```

**Usage:**
- Double-click `build.bat` to rebuild
- Double-click `run.bat` to start the proxy

---

## Troubleshooting

### Problem: `go: command not found`

**Cause:** Go is not installed or not in PATH

**Solution:**
1. Install Go from https://go.dev/dl/
2. Add to PATH (see Prerequisites section)
3. **Restart** Command Prompt/PowerShell
4. Verify: `go version`

### Problem: Files in Wrong Folder Structure

**Symptoms:**
```
go build -o xapi-proxy.exe cmd/proxy/main.go
package cmd/proxy/main.go is not in std
```

**Cause:** Files were downloaded individually or extraction didn't preserve folders

**Solution:**

```powershell
# Create proper structure
New-Item -ItemType Directory -Force -Path cmd\proxy
New-Item -ItemType Directory -Force -Path internal\config
New-Item -ItemType Directory -Force -Path internal\handlers
New-Item -ItemType Directory -Force -Path internal\middleware
New-Item -ItemType Directory -Force -Path internal\models
New-Item -ItemType Directory -Force -Path internal\store
New-Item -ItemType Directory -Force -Path internal\validator

# Move files to correct locations
Move-Item -Path main.go -Destination cmd\proxy\ -Force
Move-Item -Path config.go -Destination internal\config\ -Force
Move-Item -Path handlers.go -Destination internal\handlers\ -Force
Move-Item -Path middleware.go -Destination internal\middleware\ -Force
Move-Item -Path token.go -Destination internal\models\ -Force
Move-Item -Path tenant.go -Destination internal\store\ -Force
Move-Item -Path permissions.go -Destination internal\validator\ -Force

# Rebuild
go mod tidy
go build -o xapi-proxy.exe ./cmd/proxy
```

### Problem: Missing Dependencies (`go.sum` errors)

**Symptoms:**
```
missing go.sum entry for module providing package...
```

**Solution:**

```powershell
# Clean and regenerate
go clean -modcache
go mod download
go mod tidy
go build -o xapi-proxy.exe ./cmd/proxy
```

### Problem: Port 8080 Already in Use

**Symptoms:**
```
bind: Only one usage of each socket address... is normally permitted
```

**Solution:**

**Option 1 - Find and kill the process:**
```cmd
netstat -ano | findstr :8080
taskkill /PID <PID_NUMBER> /F
```

**Option 2 - Use different port:**
```powershell
.\xapi-proxy.exe --config config.yaml --port 8081
```

### Problem: Can't Connect to LRS

**Solution:**

Use the public test LRS in `config.yaml`:
```yaml
lrs:
  endpoint: "https://lrs.adlnet.gov/xapi/"
  username: "test"
  password: "test"
```

### Problem: Permission Denied When Building

**Cause:** Antivirus blocking Go compiler or executable

**Solution:**
- Add exception for Go installation directory
- Add exception for your project directory
- Temporarily disable antivirus during build

---

## Next Steps

### Test with Real Content

1. Configure your LRS endpoint in `config.yaml`
2. Point your LMS to use the proxy:
   - Token API: `http://localhost:8080/auth/token`
   - xAPI Endpoint: `http://localhost:8080/xapi/`
3. Launch cmi5 content from your LMS

### Deploy to Production

See `ARCHITECTURE.md` for deployment options:
- Windows Server with IIS reverse proxy
- Docker on Windows Server
- Azure Container Instances
- AWS EC2 Windows instance

### Enable Multi-Tenant Mode

See `README.md` for PostgreSQL setup and multi-tenant configuration.

---

## Common Windows-Specific Notes

### File Paths

Windows uses backslashes `\` in paths:
```powershell
# Correct
cd E:\projects\xapi-lrs-auth-proxy

# Also works (PowerShell handles both)
cd E:/projects/xapi-lrs-auth-proxy
```

### Running Executables

Always include `.\` prefix:
```powershell
# Correct
.\xapi-proxy.exe

# Won't work
xapi-proxy.exe
```

### Environment Variables

Set via System Properties or in PowerShell:
```powershell
# Temporary (current session only)
$env:JWT_SECRET = "your-secret-here"

# Permanent (requires admin)
[Environment]::SetEnvironmentVariable("JWT_SECRET", "your-secret", "Machine")
```

### Firewall

Windows Firewall may prompt on first run:
- Click **"Allow access"** when prompted
- Or manually add exception for port 8080

---

## Additional Resources

- **Main Documentation:** [README.md](README.md)
- **Architecture:** [ARCHITECTURE.md](ARCHITECTURE.md)
- **Testing Guide:** [TESTING.md](TESTING.md)
- **Contributing:** [CONTRIBUTING.md](CONTRIBUTING.md)

## Getting Help

- **GitHub Issues:** https://github.com/tla-ecosystem/xapi-lrs-auth-proxy/issues
- **cmi5 Working Group:** IEEE LTSC cmi5 working group
- **TLA Forum:** https://discuss.tlaworks.com/

---

**Success!** You should now have a working xAPI LRS Auth Proxy on Windows.

For questions or issues specific to Windows, please open a GitHub issue with the "windows" label.
