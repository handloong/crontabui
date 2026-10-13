package crontab

import (
	"fmt"
	"strconv"
	"strings"
)

// 字段定义：分钟、小时、日、月、星期
var (
	fieldNames = []string{"分钟", "小时", "日", "月", "星期"}
	fieldMin   = []int{0, 0, 1, 1, 0}
	fieldMax   = []int{59, 23, 31, 12, 7}
)

var monthNames = map[string]int{
	"JAN": 1, "FEB": 2, "MAR": 3, "APR": 4, "MAY": 5, "JUN": 6,
	"JUL": 7, "AUG": 8, "SEP": 9, "OCT": 10, "NOV": 11, "DEC": 12,
}

var dowNames = map[string]int{
	"SUN": 0, "MON": 1, "TUE": 2, "WED": 3, "THU": 4, "FRI": 5, "SAT": 6,
}

// 支持的 @ 宏，值为中文描述
var macros = map[string]string{
	"@reboot":   "系统启动时",
	"@yearly":   "每年 1 月 1 日 00:00",
	"@annually": "每年 1 月 1 日 00:00",
	"@monthly":  "每月 1 日 00:00",
	"@weekly":   "每周日 00:00",
	"@daily":    "每天 00:00",
	"@midnight": "每天 00:00",
	"@hourly":   "每小时整点",
}

// ValidateSchedule 校验 cron 表达式：支持标准 5 字段写法与 @ 宏。
func ValidateSchedule(expr string) error {
	e := strings.TrimSpace(expr)
	if e == "" {
		return fmt.Errorf("表达式为空")
	}
	if strings.HasPrefix(e, "@") {
		if _, ok := macros[strings.ToLower(e)]; !ok {
			return fmt.Errorf("不支持的宏 %q，仅支持 @reboot @hourly @daily @midnight @weekly @monthly @yearly @annually", e)
		}
		return nil
	}
	fields := strings.Fields(e)
	if len(fields) != 5 {
		return fmt.Errorf("表达式必须包含 5 个字段（分 时 日 月 周）或使用 @ 宏，当前为 %d 个字段", len(fields))
	}
	for i, f := range fields {
		if err := validateField(i, f); err != nil {
			return err
		}
	}
	return nil
}

func validateField(idx int, raw string) error {
	name := fieldNames[idx]
	if raw == "" {
		return fmt.Errorf("%s字段为空", name)
	}
	for _, part := range strings.Split(raw, ",") {
		if part == "" {
			return fmt.Errorf("%s字段存在为空的分段，请检查逗号", name)
		}
		body := part
		if i := strings.Index(part, "/"); i >= 0 {
			body = part[:i]
			stepStr := part[i+1:]
			step, err := strconv.Atoi(stepStr)
			if err != nil || step <= 0 {
				return fmt.Errorf("%s字段的步长 %q 无效，必须为正整数（如 */5）", name, stepStr)
			}
			if body == "" {
				return fmt.Errorf("%s字段的步长写法 %q 无效，缺少基础值（如 */5）", name, part)
			}
		}
		if body == "*" {
			continue
		}
		if i := strings.Index(body, "-"); i > 0 {
			if strings.Count(body, "-") > 1 {
				return fmt.Errorf("%s字段的值 %q 无效", name, body)
			}
			lo, err := parseFieldValue(idx, body[:i])
			if err != nil {
				return err
			}
			hi, err := parseFieldValue(idx, body[i+1:])
			if err != nil {
				return err
			}
			if lo > hi {
				return fmt.Errorf("%s字段的范围 %q 无效，起始值不能大于结束值", name, body)
			}
			continue
		}
		if _, err := parseFieldValue(idx, body); err != nil {
			return err
		}
	}
	return nil
}

func parseFieldValue(idx int, s string) (int, error) {
	name := fieldNames[idx]
	up := strings.ToUpper(s)
	if idx == 3 {
		if v, ok := monthNames[up]; ok {
			return v, nil
		}
	}
	if idx == 4 {
		if v, ok := dowNames[up]; ok {
			return v, nil
		}
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0, fmt.Errorf("%s字段的值 %q 无效，应为数字或合法名称", name, s)
	}
	if n < fieldMin[idx] || n > fieldMax[idx] {
		return 0, fmt.Errorf("%s字段的值 %d 超出范围 %d-%d", name, n, fieldMin[idx], fieldMax[idx])
	}
	return n, nil
}

// DescribeSchedule 返回表达式的中文可读描述；无法识别时原样返回表达式。
func DescribeSchedule(expr string) string {
	e := strings.TrimSpace(expr)
	if d, ok := macros[strings.ToLower(e)]; ok {
		return d
	}
	f := strings.Fields(e)
	if len(f) != 5 {
		return e
	}
	minute, hour, dom, month, dow := f[0], f[1], f[2], f[3], f[4]

	constAll := func(fields ...string) bool {
		for _, x := range fields {
			if x != "*" {
				return false
			}
		}
		return true
	}

	if constAll(minute, hour, dom, month, dow) {
		return "每分钟"
	}

	// 每 N 分钟
	if hour == "*" && dom == "*" && month == "*" && dow == "*" {
		if n, ok := stepValue(minute, "*"); ok {
			if n == 1 {
				return "每分钟"
			}
			return fmt.Sprintf("每 %d 分钟", n)
		}
		if minute == "0" {
			return "每小时整点"
		}
		if n, ok := plainInt(minute); ok {
			return fmt.Sprintf("每小时第 %d 分钟", n)
		}
	}

	h, okH := plainInt(hour)
	m, okM := plainInt(minute)
	if !okH || !okM {
		return e
	}
	clock := fmt.Sprintf("%02d:%02d", h, m)

	if month != "*" {
		if mm, ok := plainInt(month); ok {
			if d, ok := plainInt(dom); ok {
				return fmt.Sprintf("每年 %d 月 %d 日 %s", mm, d, clock)
			}
		}
		return e
	}
	if dom != "*" {
		if d, ok := plainInt(dom); ok && dow == "*" {
			return fmt.Sprintf("每月 %d 日 %s", d, clock)
		}
		return e
	}
	if dow != "*" {
		names, ok := weekNames(dow)
		if !ok {
			return e
		}
		return fmt.Sprintf("每周%s %s", strings.Join(names, "、周"), clock)
	}
	return "每天 " + clock
}

// stepValue 解析 "*/N" 形式，返回 N。
func stepValue(expr, base string) (int, bool) {
	if expr == base {
		return 1, true
	}
	prefix := base + "/"
	if !strings.HasPrefix(expr, prefix) {
		return 0, false
	}
	n, err := strconv.Atoi(expr[len(prefix):])
	if err != nil {
		return 0, false
	}
	return n, true
}

func plainInt(s string) (int, bool) {
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0, false
	}
	return n, true
}

func weekNames(expr string) ([]string, bool) {
	var out []string
	for _, part := range strings.Split(expr, ",") {
		up := strings.ToUpper(strings.TrimSpace(part))
		var n int
		if v, ok := dowNames[up]; ok {
			n = v
		} else {
			v, err := strconv.Atoi(up)
			if err != nil || v < 0 || v > 7 {
				return nil, false
			}
			n = v
		}
		out = append(out, weekName(n))
	}
	if len(out) == 0 {
		return nil, false
	}
	return out, true
}

func weekName(n int) string {
	switch n % 7 {
	case 0:
		return "日"
	case 1:
		return "一"
	case 2:
		return "二"
	case 3:
		return "三"
	case 4:
		return "四"
	case 5:
		return "五"
	default:
		return "六"
	}
}

// CommandWarning 检查命令中会被 cron 特殊处理且可能造成意外的字符。
func CommandWarning(cmd string) string {
	for i := 0; i < len(cmd); i++ {
		if cmd[i] != '%' {
			continue
		}
		backslashes := 0
		for j := i - 1; j >= 0 && cmd[j] == '\\'; j-- {
			backslashes++
		}
		if backslashes%2 == 0 {
			return `命令中的 % 会被 cron 转换为换行符，如需字面量请写成 \%`
		}
	}
	return ""
}
