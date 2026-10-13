package crontab

import (
	"strings"
	"testing"
)

func TestValidateScheduleValid(t *testing.T) {
	cases := []string{
		"* * * * *",
		"*/5 * * * *",
		"0 3 * * *",
		"0 0 * * MON",
		"0 0 * * mon-fri",
		"0 0 * * sat,sun",
		"1-20/2 0 1 JAN *",
		"0 0,12 * * 1,3,5",
		"59 23 31 12 7",
		"0 0 * dec 0",
		"  0 3 * * *  ",
		"@reboot",
		"@hourly",
		"@daily",
		"@midnight",
		"@weekly",
		"@monthly",
		"@yearly",
		"@annually",
	}
	for _, c := range cases {
		if err := ValidateSchedule(c); err != nil {
			t.Errorf("ValidateSchedule(%q) = %v, want nil", c, err)
		}
	}
}

func TestValidateScheduleInvalid(t *testing.T) {
	cases := []struct {
		expr    string
		wantSub string
	}{
		{"", "空"},
		{"   ", "空"},
		{"* * * *", "字段"},
		{"* * * * * *", "字段"},
		{"70 * * * *", "分钟"},
		{"* 25 * * *", "小时"},
		{"0 0 32 * *", "日"},
		{"0 0 0 * *", "日"},
		{"0 0 * 13 *", "月"},
		{"0 0 * 0 *", "月"},
		{"0 0 * * 9", "星期"},
		{"*/0 * * * *", "步长"},
		{"*/x * * * *", "步长"},
		{"abc * * * *", "分钟"},
		{"5-1 * * * *", "范围"},
		{"0 0 * * FOO", "星期"},
		{"0 0 * FOO *", "月"},
		{"@every 5m", "宏"},
		{"@nope", "宏"},
	}
	for _, c := range cases {
		err := ValidateSchedule(c.expr)
		if err == nil {
			t.Errorf("ValidateSchedule(%q) = nil, want error containing %q", c.expr, c.wantSub)
			continue
		}
		if !strings.Contains(err.Error(), c.wantSub) {
			t.Errorf("ValidateSchedule(%q) = %q, want it to contain %q", c.expr, err.Error(), c.wantSub)
		}
	}
}

func TestDescribeSchedule(t *testing.T) {
	cases := []struct {
		expr string
		want string
	}{
		{"* * * * *", "每分钟"},
		{"*/5 * * * *", "每 5 分钟"},
		{"*/1 * * * *", "每分钟"},
		{"0 * * * *", "每小时整点"},
		{"30 * * * *", "每小时第 30 分钟"},
		{"0 3 * * *", "每天 03:00"},
		{"30 4 * * *", "每天 04:30"},
		{"0 0 * * 1", "每周一 00:00"},
		{"0 9 * * MON", "每周一 09:00"},
		{"0 0 * * 0", "每周日 00:00"},
		{"0 0 * * 7", "每周日 00:00"},
		{"0 0 1 * *", "每月 1 日 00:00"},
		{"0 0 1 1 *", "每年 1 月 1 日 00:00"},
		{"@reboot", "系统启动时"},
		{"@hourly", "每小时整点"},
		{"@daily", "每天 00:00"},
		{"@midnight", "每天 00:00"},
		{"@weekly", "每周日 00:00"},
		{"@monthly", "每月 1 日 00:00"},
		{"@yearly", "每年 1 月 1 日 00:00"},
		{"0 0 * * 2,4", "每周二、周四 00:00"},
	}
	for _, c := range cases {
		if got := DescribeSchedule(c.expr); got != c.want {
			t.Errorf("DescribeSchedule(%q) = %q, want %q", c.expr, got, c.want)
		}
	}
}

func TestCommandWarnings(t *testing.T) {
	if w := CommandWarning(`echo "100%" >> /tmp/a.log`); w == "" {
		t.Error("CommandWarning should warn about unescaped % but returned empty")
	}
	if w := CommandWarning(`echo "100\%"`); w != "" {
		t.Errorf("CommandWarning should stay silent for escaped %%, got %q", w)
	}
	if w := CommandWarning(`/bin/true`); w != "" {
		t.Errorf("CommandWarning should stay silent for plain command, got %q", w)
	}
}
