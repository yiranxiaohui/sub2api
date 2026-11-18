package service

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"unicode/utf16"

	"github.com/tidwall/gjson"
)

// fingerprintSalt 是计算 cc_version 后缀指纹的盐值。
//
// 来源：Parrot src/transform/cc_mimicry.py；使用 CLI 2.1.327 抓包复核。
// 字段一致性不等同于任何上游账户风控结论。
const fingerprintSalt = "59cf53e54c78"

// computeClaudeCodeFingerprint 复刻真实 Claude Code CLI 的 cc_version 指纹算法：
//
//  1. 取 messages 中第一条 role=user 的首个非 system-reminder 文本块
//  2. 按 JavaScript UTF-16 码元取零基下标 4、7、20（不足以 '0' 补齐）
//  3. 拼接采样码元后按 JavaScript UTF-8 语义编码，再对 SALT + chars + cc_version
//     计算 SHA256，取 hex 前 3 字符
//
// 算法来自 Parrot src/transform/cc_mimicry.py:compute_fingerprint，与官方 CLI 字节对齐。
// 任何偏差都会导致 cc_version=X.Y.Z.{fp} 在上游侧与真实 CLI 不一致。
func computeClaudeCodeFingerprint(body []byte, version string) string {
	firstText := extractBillingUserText(body)
	indices := [...]int{4, 7, 20}
	// Only the prefix through index 20 is needed, even for very long prompts.
	textUnits := make([]uint16, 0, 22)
	for _, r := range firstText {
		textUnits = utf16.AppendRune(textUnits, r)
		if len(textUnits) > indices[len(indices)-1] {
			break
		}
	}
	chars := make([]uint16, 0, len(indices))
	for _, i := range indices {
		if i < len(textUnits) {
			chars = append(chars, textUnits[i])
		} else {
			chars = append(chars, '0')
		}
	}
	// Decode after joining: sampled halves can form a new surrogate pair.
	// Unpaired halves become U+FFFD, matching JS UTF-8 encoding for the hash.
	sample := string(utf16.Decode(chars))
	sum := sha256.Sum256([]byte(fingerprintSalt + sample + version))
	return hex.EncodeToString(sum[:])[:3]
}

// extractBillingUserText skips CLI-injected reminder blocks for billing only.
// Keep session-key extraction unchanged: changing extractFirstUserText's
// semantics would also remap existing sticky-session keys.
func extractBillingUserText(body []byte) string {
	first := ""
	gjson.GetBytes(body, "messages").ForEach(func(_, msg gjson.Result) bool {
		if msg.Get("role").String() != "user" {
			return true
		}
		content := msg.Get("content")
		if content.Type == gjson.String {
			first = content.String()
		} else if content.IsArray() {
			content.ForEach(func(_, block gjson.Result) bool {
				text := block.Get("text")
				if block.Get("type").String() == "text" && text.Type == gjson.String &&
					!strings.HasPrefix(text.String(), "<system-reminder>") {
					first = text.String()
					return false
				}
				return true
			})
		}
		return false
	})
	return first
}

// extractFirstUserText 提取 messages 中第一条 user 消息的首段 text 内容。
// 兼容 string 和 []block 两种 content 格式。
func extractFirstUserText(body []byte) string {
	messages := gjson.GetBytes(body, "messages")
	if !messages.IsArray() {
		return ""
	}
	first := ""
	messages.ForEach(func(_, msg gjson.Result) bool {
		if msg.Get("role").String() != "user" {
			return true
		}
		content := msg.Get("content")
		if content.Type == gjson.String {
			first = content.String()
			return false
		}
		if content.IsArray() {
			content.ForEach(func(_, block gjson.Result) bool {
				if block.Get("type").String() == "text" {
					first = block.Get("text").String()
					return false
				}
				return true
			})
			return false
		}
		return false
	})
	return first
}

// buildBillingAttributionText 构造 system 数组的 billing attribution 文本。
//
// 形态对齐真实 Claude Code CLI：
//
//	x-anthropic-billing-header: cc_version=2.1.161.{fp}; cc_entrypoint=cli;
//
// CLI 2.1.327 自定义 base URL 抓包使用此简化形态；官方直连的登录态请求
// 仍可含 cch 及回合字段。这里只构造简化块，不宣称复现所有账号开关或直连协议。
//
// 此 block 不带 cache_control（与真实 CLI 一致；cache breakpoint 由后续的
// Claude Code prompt block 承担）。
func buildBillingAttributionText(body []byte, cliVersion string) (string, error) {
	if cliVersion == "" {
		return "", fmt.Errorf("cliVersion required")
	}
	fp := computeClaudeCodeFingerprint(body, cliVersion)
	return fmt.Sprintf(
		"x-anthropic-billing-header: cc_version=%s.%s; cc_entrypoint=cli;",
		cliVersion, fp,
	), nil
}
