// 交互输入与 schema 辅助（add 向导）。
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

var stdinReader = bufio.NewReader(os.Stdin)

func ask(prompt string) string {
	fmt.Print(prompt)
	line, _ := stdinReader.ReadString('\n')
	return strings.TrimRight(line, "\r\n")
}

func askDefault(prompt, def string) string {
	line := ask(prompt)
	if line == "" {
		return def
	}
	return line
}

func newReader() *bufio.Reader { return stdinReader }

func maybeDesc(desc string) string {
	if desc == "" {
		return ""
	}
	return ": " + desc
}

// schemaProps 从 configSchema JSON 里取 properties（宽松解析）。
func schemaProps(raw []byte) map[string]struct {
	Type        string `json:"type"`
	Description string `json:"description"`
} {
	var schema struct {
		Properties map[string]struct {
			Type        string `json:"type"`
			Description string `json:"description"`
		} `json:"properties"`
	}
	if json.Unmarshal(raw, &schema) != nil {
		return nil
	}
	return schema.Properties
}

// coerce 按声明的类型转换输入。
func coerce(raw, typ string) any {
	switch typ {
	case "number", "integer":
		if v, err := strconv.Atoi(raw); err == nil {
			return v
		}
		if v, err := strconv.ParseFloat(raw, 64); err == nil {
			return v
		}
		return raw
	case "boolean":
		return raw == "true" || raw == "yes"
	default:
		return raw
	}
}

// unmarshalLoose 宽松解析用户输入的 options（YAML/JSON 兼容——yaml.v3 是 JSON 超集）。
func unmarshalLoose(raw string, out *map[string]any) error {
	var data map[string]any
	if err := json.Unmarshal([]byte(raw), &data); err == nil {
		*out = data
		return nil
	}
	var yamlData map[string]any
	if err := yaml.Unmarshal([]byte(raw), &yamlData); err != nil {
		return fmt.Errorf("options 解析失败: %w", err)
	}
	*out = yamlData
	return nil
}
