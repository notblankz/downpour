APP_NAME = downpour
CMD_PATH = .
OUT_DIR = builds
LDFLAGS = -ldflags="-s -w" -trimpath

.PHONY: all clean

all: $(OUT_DIR) linux-amd64 linux-arm64 darwin-amd64 darwin-arm64 windows-amd64 windows-arm64

$(OUT_DIR):
	mkdir -p $(OUT_DIR)

linux-amd64:
	GOOS=linux GOARCH=amd64 go build $(LDFLAGS) -o $(OUT_DIR)/$(APP_NAME)-linux-amd64 $(CMD_PATH)

linux-arm64:
	GOOS=linux GOARCH=arm64 go build $(LDFLAGS) -o $(OUT_DIR)/$(APP_NAME)-linux-arm64 $(CMD_PATH)

darwin-amd64:
	GOOS=darwin GOARCH=amd64 go build $(LDFLAGS) -o $(OUT_DIR)/$(APP_NAME)-darwin-amd64 $(CMD_PATH)

darwin-arm64:
	GOOS=darwin GOARCH=arm64 go build $(LDFLAGS) -o $(OUT_DIR)/$(APP_NAME)-darwin-arm64 $(CMD_PATH)

windows-amd64:
	GOOS=windows GOARCH=amd64 go build $(LDFLAGS) -o $(OUT_DIR)/$(APP_NAME)-windows-amd64.exe $(CMD_PATH)

windows-arm64:
	GOOS=windows GOARCH=arm64 go build $(LDFLAGS) -o $(OUT_DIR)/$(APP_NAME)-windows-arm64.exe $(CMD_PATH)

clean:
	rm -rf $(OUT_DIR)/*