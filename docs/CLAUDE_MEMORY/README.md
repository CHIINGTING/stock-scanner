# Claude 專案記憶備份

這個目錄是 `~/.claude/projects/-Users-deep-huang-stock-scanner/memory/` 的副本。

## 為什麼要備份

那個目錄在 **repo 之外**，git 完全看不到它。它保存的是幾個月來累積的決策脈絡——
scanner 的定位、guardrail A/B 的決議、R6 回測的狀態、自主執行的規則邊界——
這些都是「為什麼這樣做」而不是「做了什麼」，程式碼與 commit 訊息都推不出來。
重灌機器就會消失。

## 這是副本，不是本體

Claude 讀寫的仍是家目錄下的那份。重灌後要恢復：

```bash
mkdir -p ~/.claude/projects/-Users-deep-huang-stock-scanner/memory
cp docs/CLAUDE_MEMORY/*.md ~/.claude/projects/-Users-deep-huang-stock-scanner/memory/
rm ~/.claude/projects/-Users-deep-huang-stock-scanner/memory/README.md
```

（`README.md` 只屬於這份備份，不要複製回去——它會被當成一則記憶。）

## 快照時間

2026-08-24。此後 Claude 對記憶的更新不會自動同步到這裡，需要重新複製。
