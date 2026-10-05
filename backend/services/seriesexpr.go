package services

import (
	"math"
	"sort"
	"strconv"
	"strings"
	"unicode"
)

// Grafana-style math over query results: "$A / $B * 100", "abs($A)", "sum($A)".
const maxExpressionLength = 256

// exprValue is either a scalar constant or a set of series.
type exprValue struct {
	scalar   float64
	series   []SeriesResult
	isScalar bool
}

type exprNode interface {
	eval(env map[string][]SeriesResult) (exprValue, error)
}

type numberNode struct{ value float64 }

type refNode struct{ ref string }

type negNode struct{ operand exprNode }

type binaryNode struct {
	op          byte
	left, right exprNode
}

type callNode struct {
	name string
	arg  exprNode
}

func (n numberNode) eval(map[string][]SeriesResult) (exprValue, error) {
	return exprValue{scalar: n.value, isScalar: true}, nil
}

func (n refNode) eval(env map[string][]SeriesResult) (exprValue, error) {
	series, ok := env[n.ref]
	if !ok {
		return exprValue{}, &ExpressionError{Err: ErrExpressionUnknownRef, Token: "$" + n.ref}
	}
	return exprValue{series: series}, nil
}

func (n negNode) eval(env map[string][]SeriesResult) (exprValue, error) {
	return binaryNode{op: '*', left: numberNode{value: -1}, right: n.operand}.eval(env)
}

func (n callNode) eval(env map[string][]SeriesResult) (exprValue, error) {
	arg, err := n.arg.eval(env)
	if err != nil {
		return exprValue{}, err
	}
	switch n.name {
	case "abs":
		if arg.isScalar {
			return exprValue{scalar: math.Abs(arg.scalar), isScalar: true}, nil
		}
		return exprValue{series: mapSeries(arg.series, math.Abs)}, nil
	case "sum":
		if arg.isScalar {
			return arg, nil
		}
		return exprValue{series: []SeriesResult{sumSeries(arg.series)}}, nil
	}
	return exprValue{}, &ExpressionError{Err: ErrExpressionFunction, Token: n.name}
}

func (n binaryNode) eval(env map[string][]SeriesResult) (exprValue, error) {
	left, err := n.left.eval(env)
	if err != nil {
		return exprValue{}, err
	}
	right, err := n.right.eval(env)
	if err != nil {
		return exprValue{}, err
	}
	apply := func(a, b float64) *float64 { return applyOp(n.op, a, b) }

	switch {
	case left.isScalar && right.isScalar:
		v := apply(left.scalar, right.scalar)
		if v == nil {
			return exprValue{scalar: math.NaN(), isScalar: true}, nil
		}
		return exprValue{scalar: *v, isScalar: true}, nil
	case left.isScalar:
		return exprValue{series: mapSeriesPtr(right.series, func(v float64) *float64 { return apply(left.scalar, v) })}, nil
	case right.isScalar:
		return exprValue{series: mapSeriesPtr(left.series, func(v float64) *float64 { return apply(v, right.scalar) })}, nil
	}
	series, err := joinSeries(left.series, right.series, apply)
	if err != nil {
		return exprValue{}, err
	}
	return exprValue{series: series}, nil
}

// applyOp returns nil where the result is not a number: JSON cannot carry NaN or Inf.
func applyOp(op byte, a, b float64) *float64 {
	var v float64
	switch op {
	case '+':
		v = a + b
	case '-':
		v = a - b
	case '*':
		v = a * b
	case '/':
		if b == 0 {
			return nil
		}
		v = a / b
	}
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return nil
	}
	return &v
}

func mapSeries(series []SeriesResult, fn func(float64) float64) []SeriesResult {
	return mapSeriesPtr(series, func(v float64) *float64 {
		out := fn(v)
		return &out
	})
}

func mapSeriesPtr(series []SeriesResult, fn func(float64) *float64) []SeriesResult {
	out := make([]SeriesResult, 0, len(series))
	for _, s := range series {
		points := make([]SeriesDataPoint, len(s.Points))
		for i, p := range s.Points {
			points[i] = SeriesDataPoint{Timestamp: p.Timestamp}
			if p.Value != nil {
				points[i].Value = fn(*p.Value)
			}
		}
		out = append(out, SeriesResult{MetricName: s.MetricName, Labels: s.Labels, Points: points})
	}
	return out
}

// sumSeries collapses every series into one; a bucket stays empty only if all are.
func sumSeries(series []SeriesResult) SeriesResult {
	sums := map[int64]*float64{}
	stamps := map[int64]SeriesDataPoint{}
	for _, s := range series {
		for _, p := range s.Points {
			key := p.Timestamp.UnixMilli()
			stamps[key] = SeriesDataPoint{Timestamp: p.Timestamp}
			if p.Value == nil {
				continue
			}
			if sums[key] == nil {
				v := *p.Value
				sums[key] = &v
			} else {
				*sums[key] += *p.Value
			}
		}
	}
	keys := make([]int64, 0, len(stamps))
	for k := range stamps {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
	points := make([]SeriesDataPoint, 0, len(keys))
	for _, k := range keys {
		points = append(points, SeriesDataPoint{Timestamp: stamps[k].Timestamp, Value: sums[k]})
	}
	return SeriesResult{Labels: map[string]string{}, Points: points}
}

// joinSeries pairs series with identical labels; a lone series pairs with every
// series opposite only when its own labels do not single out one of them.
func joinSeries(left, right []SeriesResult, apply func(a, b float64) *float64) ([]SeriesResult, error) {
	type pair struct {
		l, r   SeriesResult
		labels map[string]string
	}
	var pairs []pair
	switch {
	case len(left) == 0 || len(right) == 0:
		return []SeriesResult{}, nil
	case len(right) == 1 && broadcasts(right[0].Labels, left):
		for _, l := range left {
			pairs = append(pairs, pair{l: l, r: right[0], labels: l.Labels})
		}
	case len(left) == 1 && broadcasts(left[0].Labels, right):
		for _, r := range right {
			pairs = append(pairs, pair{l: left[0], r: r, labels: r.Labels})
		}
	default:
		byKey := map[string]SeriesResult{}
		for _, r := range right {
			byKey[labelsKey(r.Labels)] = r
		}
		for _, l := range left {
			if r, ok := byKey[labelsKey(l.Labels)]; ok {
				pairs = append(pairs, pair{l: l, r: r, labels: l.Labels})
			}
		}
		// Matching nothing draws an empty chart and hides the mistake, so it is an
		// error; a partial overlap still draws what matched, which is the normal
		// shape of an error ratio: a route without errors has no series to pair.
		if len(pairs) == 0 {
			return nil, ErrExpressionNoMatch
		}
	}

	out := make([]SeriesResult, 0, len(pairs))
	for _, p := range pairs {
		rightAt := map[int64]*float64{}
		for _, pt := range p.r.Points {
			rightAt[pt.Timestamp.UnixMilli()] = pt.Value
		}
		points := make([]SeriesDataPoint, len(p.l.Points))
		for i, pt := range p.l.Points {
			points[i] = SeriesDataPoint{Timestamp: pt.Timestamp}
			if rv := rightAt[pt.Timestamp.UnixMilli()]; pt.Value != nil && rv != nil {
				points[i].Value = apply(*pt.Value, *rv)
			}
		}
		out = append(out, SeriesResult{Labels: p.labels, Points: points})
	}
	return out, nil
}

// broadcasts reports whether a lone series stands for the whole set opposite: its
// labels must hold on every series there, so a total pairs with each group but one
// surviving route does not pair with the other routes.
func broadcasts(lone map[string]string, others []SeriesResult) bool {
	for _, o := range others {
		for key, value := range lone {
			if o.Labels[key] != value {
				return false
			}
		}
	}
	return true
}

func labelsKey(labels map[string]string) string {
	return strings.Join(encodeLabels(labels), "\x00")
}

// EvaluateExpression applies a parsed expression to the results of each query ref.
func EvaluateExpression(expression string, env map[string][]SeriesResult) ([]SeriesResult, error) {
	node, err := ParseExpression(expression)
	if err != nil {
		return nil, err
	}
	value, err := node.eval(env)
	if err != nil {
		return nil, err
	}
	if value.isScalar {
		return nil, ErrExpressionScalar
	}
	for i := range value.series {
		value.series[i].MetricName = expression
		if value.series[i].Labels == nil {
			value.series[i].Labels = map[string]string{}
		}
	}
	return value.series, nil
}

// ParseExpression parses "+ - * /", parentheses, numbers, $REF and abs()/sum().
func ParseExpression(expression string) (exprNode, error) {
	if len(expression) > maxExpressionLength {
		return nil, ErrExpressionTooLong
	}
	p := &exprParser{input: expression}
	node, err := p.parseSum()
	if err != nil {
		return nil, err
	}
	p.skipSpaces()
	if p.pos < len(p.input) {
		return nil, p.syntaxError()
	}
	return node, nil
}

type exprParser struct {
	input string
	pos   int
}

func (p *exprParser) syntaxError() error {
	return &ExpressionError{Err: ErrExpressionSyntax, Position: p.pos + 1}
}

func (p *exprParser) skipSpaces() {
	for p.pos < len(p.input) && p.input[p.pos] == ' ' {
		p.pos++
	}
}

func (p *exprParser) peek() byte {
	p.skipSpaces()
	if p.pos >= len(p.input) {
		return 0
	}
	return p.input[p.pos]
}

func (p *exprParser) parseSum() (exprNode, error) {
	left, err := p.parseProduct()
	if err != nil {
		return nil, err
	}
	for op := p.peek(); op == '+' || op == '-'; op = p.peek() {
		p.pos++
		right, err := p.parseProduct()
		if err != nil {
			return nil, err
		}
		left = binaryNode{op: op, left: left, right: right}
	}
	return left, nil
}

func (p *exprParser) parseProduct() (exprNode, error) {
	left, err := p.parseUnary()
	if err != nil {
		return nil, err
	}
	for op := p.peek(); op == '*' || op == '/'; op = p.peek() {
		p.pos++
		right, err := p.parseUnary()
		if err != nil {
			return nil, err
		}
		left = binaryNode{op: op, left: left, right: right}
	}
	return left, nil
}

func (p *exprParser) parseUnary() (exprNode, error) {
	if p.peek() == '-' {
		p.pos++
		operand, err := p.parseUnary()
		if err != nil {
			return nil, err
		}
		return negNode{operand: operand}, nil
	}
	return p.parsePrimary()
}

func (p *exprParser) parsePrimary() (exprNode, error) {
	c := p.peek()
	switch {
	case c == '(':
		p.pos++
		node, err := p.parseSum()
		if err != nil {
			return nil, err
		}
		if p.peek() != ')' {
			return nil, p.syntaxError()
		}
		p.pos++
		return node, nil
	case c == '$':
		p.pos++
		ref := p.readWhile(func(r byte) bool { return r >= 'A' && r <= 'Z' })
		if len(ref) != 1 {
			return nil, p.syntaxError()
		}
		return refNode{ref: ref}, nil
	case c == '.' || (c >= '0' && c <= '9'):
		start := p.pos
		literal := p.readWhile(func(r byte) bool { return r == '.' || (r >= '0' && r <= '9') })
		value, err := strconv.ParseFloat(literal, 64)
		if err != nil {
			p.pos = start
			return nil, p.syntaxError()
		}
		return numberNode{value: value}, nil
	case unicode.IsLetter(rune(c)):
		name := p.readWhile(func(r byte) bool { return unicode.IsLetter(rune(r)) })
		if name != "abs" && name != "sum" {
			return nil, &ExpressionError{Err: ErrExpressionFunction, Token: name}
		}
		if p.peek() != '(' {
			return nil, p.syntaxError()
		}
		p.pos++
		arg, err := p.parseSum()
		if err != nil {
			return nil, err
		}
		if p.peek() != ')' {
			return nil, p.syntaxError()
		}
		p.pos++
		return callNode{name: name, arg: arg}, nil
	}
	return nil, p.syntaxError()
}

func (p *exprParser) readWhile(match func(byte) bool) string {
	start := p.pos
	for p.pos < len(p.input) && match(p.input[p.pos]) {
		p.pos++
	}
	return p.input[start:p.pos]
}
