package sanitize

import (
	"github.com/viant/datly/cmd/matchers"
	"github.com/viant/parsly"
	"github.com/viant/parsly/matcher"
)

const (
	whitespaceToken int = iota
	wordToken
	commentBlockToken
	selectorStartToken
	parenthesesBlockToken
	comaTerminatorToken

	anyToken
	endToken
	elseToken
	assignToken
	forEachToken
	ifToken

	numberToken
	boolToken
	stringToken
	scopeBlockToken
	selectorToken

	insertToken
	intoToken
	valuesToken
)

var whitespaceMatcher = parsly.NewToken(whitespaceToken, "Whitespace", matcher.NewWhiteSpace())
var fullWordMatcher = parsly.NewToken(wordToken, "Word", matchers.NewWordMatcher(true))
var commentBlockMatcher = parsly.NewToken(commentBlockToken, "Comment", matcher.NewSeqBlock("/*", "*/"))
var selectorStartMatcher = parsly.NewToken(selectorStartToken, "Selector start", matcher.NewByte('$'))
var comaTerminatorMatcher = parsly.NewToken(comaTerminatorToken, "coma", matcher.NewTerminator(',', true))
var parenthesesBlockMatcher = parsly.NewToken(parenthesesBlockToken, "Parentheses", matcher.NewBlock('(', ')', '\\'))

var anyMatcher = parsly.NewToken(anyToken, "Any", matchers.NewAny())
var endMatcher = parsly.NewToken(endToken, "End", matcher.NewFragment("#end"))
var elseMatcher = parsly.NewToken(elseToken, "Else", matcher.NewFragment("#else"))
var elseIfMatcher = parsly.NewToken(elseToken, "ElseIf", matcher.NewFragment("#elseif"))
var assignMatcher = parsly.NewToken(assignToken, "Set", matcher.NewFragment("#set"))
var forEachMatcher = parsly.NewToken(forEachToken, "ForEach", matcher.NewFragment("#foreach"))
var ifMatcher = parsly.NewToken(ifToken, "If", matcher.NewFragment("#if"))

var numberMatcher = parsly.NewToken(numberToken, "Number", matcher.NewNumber())
var boolMatcher = parsly.NewToken(boolToken, "Boolean", matcher.NewFragmentsFold([]byte("true"), []byte("false")))
var boolTokenMatcher = parsly.NewToken(boolToken, "Boolean", matcher.NewFragments(
	[]byte("&&"), []byte("||"),
))

var singleQuoteStringMatcher = parsly.NewToken(stringToken, "String", matcher.NewBlock('\'', '\'', '\\'))
var doubleQuoteStringMatcher = parsly.NewToken(stringToken, "String", matcher.NewBlock('"', '"', '\\'))
var backtickQuoteStringMatcher = parsly.NewToken(stringToken, "String", matcher.NewBlock('`', '`', '\\'))

var scopeBlockMatcher = parsly.NewToken(scopeBlockToken, "{ .... }", matcher.NewBlock('{', '}', '\\'))
var selectorMatcher = parsly.NewToken(selectorToken, "selector", matchers.NewSelector())

var insertMatcher = parsly.NewToken(insertToken, "insert", matcher.NewFragmentsFold([]byte("insert")))
var intoMatcher = parsly.NewToken(intoToken, "into", matcher.NewFragmentsFold([]byte("into")))
var valuesMatcher = parsly.NewToken(valuesToken, "values", matcher.NewFragmentsFold([]byte("values")))
