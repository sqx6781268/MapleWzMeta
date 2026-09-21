package wzxml

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"
)

// ErrNonUTF8Content 表示文件声明了非 UTF-8 编码且确实含多字节内容，
// 需要引入 GB18030 解码器才能安全读取；本包不会静默按 UTF-8 猜测。
var ErrNonUTF8Content = errors.New("wzxml: 文件声明非 UTF-8 编码且含多字节内容")

// RepairInfo 记录一次容错解码对源文本做的改动，供上层登记与告警。
type RepairInfo struct {
	// RepairedTags 是补全了自闭合符的截断标签行数。
	RepairedTags int
	// DeclaredEncoding 是 XML 声明里的 encoding 值，为空表示未声明。
	DeclaredEncoding string
	// NormalizedEncoding 表示把声明改写成了 UTF-8（仅当内容确认为纯 ASCII）。
	NormalizedEncoding bool
}

var declRe = regexp.MustCompile(`encoding="[^"]*"`)

// DecodeFileRobust 在 Decode 基础上处理两类实测遇到的脏数据：
//
//  1. XML 声明写作 gb18030 等编码：内容确认为纯 ASCII 时按 UTF-8 读取，否则返回 ErrNonUTF8Content；
//  2. 行尾被截断的标签（如导出器写出的 `  <sound name="Subway"`，缺少 value 与 `/>`）：
//     仅对以引号收尾、未以 `>` 收尾的标签行补 `/>`，不做通用 HTML 纠错。
//
// 修复只发生在内存副本，源文件保持原样。
func DecodeFileRobust(path string) (*Node, RepairInfo, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, RepairInfo{}, err
	}
	info := RepairInfo{}
	raw = bytes.TrimPrefix(raw, []byte("\xef\xbb\xbf"))

	head := raw
	if len(head) > 120 {
		head = head[:120]
	}
	if m := regexp.MustCompile(`encoding="([^"]*)"`).FindSubmatch(head); m != nil {
		info.DeclaredEncoding = string(m[1])
		if !strings.EqualFold(info.DeclaredEncoding, "utf-8") {
			if !isASCIIRaw(raw) {
				return nil, info, fmt.Errorf("%s: %w（声明 %s）", path, ErrNonUTF8Content, info.DeclaredEncoding)
			}
			raw = append(declRe.ReplaceAll(raw[:len(head)], []byte(`encoding="UTF-8"`)), raw[len(head):]...)
			info.NormalizedEncoding = true
		}
	}

	n, firstErr := Decode(bytes.NewReader(raw))
	if firstErr == nil {
		return n, info, nil
	}

	repaired, count := repairTruncatedTags(raw)
	if count > 0 {
		if n2, err2 := Decode(bytes.NewReader(repaired)); err2 == nil {
			info.RepairedTags = count
			return n2, info, nil
		}
	}
	return nil, info, fmt.Errorf("%s: %w", path, firstErr)
}

func repairTruncatedTags(raw []byte) ([]byte, int) {
	lines := strings.Split(string(raw), "\n")
	count := 0
	for i, ln := range lines {
		body := strings.TrimRight(ln, "\r")
		head := strings.TrimLeft(body, " \t")
		if len(head) < 3 || !strings.HasPrefix(head, "<") || strings.HasPrefix(head, "</") ||
			strings.HasPrefix(head, "<?") || strings.HasSuffix(body, ">") {
			continue
		}
		if !strings.HasSuffix(body, `"`) {
			continue
		}
		suffix := ln[len(body):]
		lines[i] = body + "/>" + suffix
		count++
	}
	return []byte(strings.Join(lines, "\n")), count
}

func isASCIIRaw(b []byte) bool {
	for _, c := range b {
		if c >= 0x80 {
			return false
		}
	}
	return true
}
