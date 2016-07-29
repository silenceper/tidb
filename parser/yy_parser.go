// Copyright 2015 PingCAP, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// See the License for the specific language governing permissions and
// limitations under the License.

package parser

import (
	"regexp"
	"strings"
	"unicode"

	"github.com/juju/errors"
	"github.com/pingcap/tidb/ast"
	"github.com/pingcap/tidb/mysql"
	"github.com/pingcap/tidb/terror"
)

// Error instances.
var (
	ErrSyntax = terror.ClassParser.New(CodeSyntaxErr, "syntax error")
)

// Error codes.
const (
	CodeSyntaxErr terror.ErrCode = 1
)

var (
	specCodePattern = regexp.MustCompile(`\/\*!(M?[0-9]{5,6})?([^*]|\*+[^*/])*\*+\/`)
	specCodeStart   = regexp.MustCompile(`^\/\*!(M?[0-9]{5,6} )?[ \t]*`)
	specCodeEnd     = regexp.MustCompile(`[ \t]*\*\/$`)
)

func trimComment(txt string) string {
	txt = specCodeStart.ReplaceAllString(txt, "")
	return specCodeEnd.ReplaceAllString(txt, "")
}

// See http://dev.mysql.com/doc/refman/5.7/en/comments.html
// Convert "/*!VersionNumber MySQL-specific-code */" to "MySQL-specific-code".
// TODO: Find a better way:
// 1. RegExpr is slow.
// 2. Handle nested comment.
func handleMySQLSpecificCode(sql string) string {
	if strings.Index(sql, "/*!") == -1 {
		// Fast way to check if text contains MySQL-specific code.
		return sql
	}
	// SQL text contains MySQL-specific code. We should convert it to normal SQL text.
	return specCodePattern.ReplaceAllStringFunc(sql, trimComment)
}

// Parser represents a parser instance. Some temporary objects are stored in it to reduce object allocation during Parse function.
type Parser struct {
	charset   string
	collation string
	result    []ast.StmtNode
	cache     []yySymType
	src       string
	lexer     yyReset
}

// yyReset is an interface with reset method and returns a yyLexer.
// if an object implements both yyLexer and yyReset, it can return itself in the reset method, thus reducing allocation.
type yyReset interface {
	reset(sql string) yyLexer
}

type defaultLexer struct{}

func (l defaultLexer) reset(sql string) yyLexer {
	return NewLexer(sql)
}

// New returns a Parser object.
func New() *Parser {
	return &Parser{
		cache: make([]yySymType, 200),
		lexer: defaultLexer{},
	}
}

// Parse parses a query string to raw ast.StmtNode.
// If charset or collation is "", default charset and collation will be used.
func (parser *Parser) Parse(sql, charset, collation string) ([]ast.StmtNode, error) {
	if charset == "" {
		charset = mysql.DefaultCharset
	}
	if collation == "" {
		collation = mysql.DefaultCollationName
	}
	parser.charset = charset
	parser.collation = collation
	parser.src = sql
	parser.result = parser.result[:0]

	sql = handleMySQLSpecificCode(sql)

	l := parser.lexer.reset(sql)
	yyParse(l, parser)

	if len(l.Errors()) != 0 {
		return nil, errors.Trace(l.Errors()[0])
	}
	return parser.result, nil
}

// ParseOneStmt parses a query and returns an ast.StmtNode.
// The query must have one statement, otherwise ErrSyntax is returned.
func (parser *Parser) ParseOneStmt(sql, charset, collation string) (ast.StmtNode, error) {
	stmts, err := parser.Parse(sql, charset, collation)
	if err != nil {
		return nil, errors.Trace(err)
	}
	if len(stmts) != 1 {
		return nil, ErrSyntax
	}
	return stmts[0], nil
}

// The select statement is not at the end of the whole statement, if the last
// field text was set from its offset to the end of the src string, update
// the last field text.
func (parser *Parser) setLastSelectFieldText(st *ast.SelectStmt, lastEnd int) {
	lastField := st.Fields.Fields[len(st.Fields.Fields)-1]
	if lastField.Offset+len(lastField.Text()) >= len(parser.src)-1 {
		lastField.SetText(parser.src[lastField.Offset:lastEnd])
	}
}

func (parser *Parser) startOffset(offset int) int {
	offset--
	for unicode.IsSpace(rune(parser.src[offset])) {
		offset++
	}
	return offset
}

func (parser *Parser) endOffset(offset int) int {
	offset--
	for offset > 0 && unicode.IsSpace(rune(parser.src[offset-1])) {
		offset--
	}
	return offset
}
