BIN  := bin/scanner
CMD  := ./cmd/scanner

.PHONY: build run run-fast run-rotation run-top100 run-top500 run-all tidy lint clean test

build:
	go build -o $(BIN) $(CMD)

# 只跑 portfolio / watchlist + 族群輪動（跳過全市場掃描）。
# 保留輪動，飆股候選才有族群資金流入連動。
run-fast: build
	./$(BIN) -config configs/config.yaml --no-market

# 只跑族群輪動（跳過全市場掃描），找下一波接棒族群
run-rotation: build
	./$(BIN) -config configs/config.yaml --no-market --sectors configs/sectors.yaml

# 市場掃描 Top 50（預設）
run: build
	./$(BIN) -config configs/config.yaml --top 50

# 市場掃描 Top 100
run-top100: build
	./$(BIN) -config configs/config.yaml --top 100

# 市場掃描 Top 500
run-top500: build
	./$(BIN) -config configs/config.yaml --top 500

# 市場掃描全部上市股票
run-all: build
	./$(BIN) -config configs/config.yaml --all

# 指定日期
run-date: build
	./$(BIN) -config configs/config.yaml --top $(or $(TOP),50) -date $(DATE)

# 指定不同的持股清單
run-stocks: build
	./$(BIN) -config configs/config.yaml --no-market --stocks $(STOCKS)

tidy:
	go mod tidy

lint:
	go vet ./...

clean:
	rm -rf bin/ reports/

test:
	go test ./...
