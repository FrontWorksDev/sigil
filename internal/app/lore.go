package app

import (
	"errors"
	"fmt"
	"io"
	"strings"
)

// loreError は、1 行目に世界観の言葉、2 行目に普通の言葉での説明と対処を出すエラー（issue #3）。
//
// 世界観の言葉だけでは何をすればよいか分からないので、元のエラー（err）の文言は必ず残し、
// 2 行目にそのまま出す。
type loreError struct {
	lore string
	err  error
}

func (e *loreError) Error() string { return e.lore + ": " + e.err.Error() }
func (e *loreError) Unwrap() error { return e.err }

// lore は err に世界観の言葉を添える。すでに添えてあれば、そのまま返す（内側の言葉を優先する）。
func lore(l string, err error) error {
	if err == nil {
		return nil
	}
	var le *loreError
	if errors.As(err, &le) {
		return err
	}
	return &loreError{lore: l, err: err}
}

// loref は lore(l, fmt.Errorf(format, a...)) の短縮。
func loref(l, format string, a ...any) error {
	return lore(l, fmt.Errorf(format, a...))
}

// printError は、エラーを "sigil: " で始めて標準エラーに出す。
// loreError なら 2 行に分け、2 行目は "sigil: " の幅だけ字下げする。
func printError(w io.Writer, err error) {
	var le *loreError
	if !errors.As(err, &le) {
		fmt.Fprintf(w, "sigil: %s\n", err)
		return
	}
	indent := strings.Repeat(" ", len("sigil: "))
	fmt.Fprintf(w, "sigil: %s\n%s%s\n", le.lore, indent, strings.ReplaceAll(le.err.Error(), "\n", "\n"+indent))
}
