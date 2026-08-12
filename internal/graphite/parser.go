package graphite

import (
	"bufio"
	"fmt"
	"strings"
)

type graph struct {
	roots []string
	nodes map[string]node
}

type node struct {
	name   string
	parent string
}

type record struct {
	name  string
	depth int
	span  int
	line  int
	root  bool
}

// parseLog accepts exactly the compact Graphite 1.8.6 output emitted by:
//
//	gt log short --all --reverse --no-interactive
//
// Each nonempty line is one branch node, optionally followed by a connector
// that opens visual lanes for children. Any other syntax is display drift.
func parseLog(output string) (graph, error) {
	var lines []string
	scanner := bufio.NewScanner(strings.NewReader(output))
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}
	if err := scanner.Err(); err != nil {
		return graph{}, fmt.Errorf("read Graphite display: %w", err)
	}
	if len(lines) == 0 {
		return graph{}, fmt.Errorf("Graphite display is empty")
	}

	var records []record
	separator := false
	for index, line := range lines {
		if line == "" {
			if separator || len(records) == 0 || index == len(lines)-1 || records[len(records)-1].span != 0 {
				return graph{}, drift(index+1, line)
			}
			separator = true
			continue
		}
		record, ok := parseRecord(line, index+1)
		if !ok {
			return graph{}, drift(index+1, line)
		}
		record.root = len(records) == 0 || separator
		records = append(records, record)
		separator = false
	}
	return buildGraph(records)
}

func buildGraph(records []record) (graph, error) {
	result := graph{nodes: make(map[string]node)}
	lastAtDepth := make(map[int]string)
	var previous *record

	for _, current := range records {
		if _, exists := result.nodes[current.name]; exists {
			return graph{}, fmt.Errorf("Graphite display repeats branch %q", current.name)
		}
		if current.root {
			if current.depth != 0 || (previous != nil && previous.span != 0) {
				return graph{}, drift(current.line, current.name)
			}
			result.nodes[current.name] = node{name: current.name}
			result.roots = append(result.roots, current.name)
		} else {
			if previous == nil {
				return graph{}, drift(current.line, current.name)
			}
			switch {
			case current.depth > previous.depth:
				if previous.span == 0 || current.depth != previous.depth+previous.span {
					return graph{}, drift(current.line, current.name)
				}
				result.nodes[current.name] = node{name: current.name, parent: previous.name}
			case current.depth == previous.depth:
				if previous.span != 0 {
					return graph{}, drift(current.line, current.name)
				}
				result.nodes[current.name] = node{name: current.name, parent: previous.name}
			case current.depth < previous.depth:
				if previous.span != 0 {
					return graph{}, drift(current.line, current.name)
				}
				parent, exists := lastAtDepth[current.depth]
				if !exists {
					return graph{}, drift(current.line, current.name)
				}
				result.nodes[current.name] = node{name: current.name, parent: parent}
			}
		}
		lastAtDepth[current.depth] = current.name
		for depth := current.depth + 1; depth <= current.depth+current.span; depth++ {
			lastAtDepth[depth] = current.name
		}
		previous = &current
	}
	if previous.span != 0 {
		return graph{}, fmt.Errorf("Graphite display ends after a fork connector")
	}
	return result, nil
}

func parseRecord(line string, lineNumber int) (record, bool) {
	remainder, depth := trimGraphPrefix(line)
	if !strings.HasPrefix(remainder, "◯") && !strings.HasPrefix(remainder, "◉") {
		return record{}, false
	}
	remainder = remainder[len("◯"):]
	span := 0
	if strings.HasPrefix(remainder, "─") {
		span = 1
		remainder = strings.TrimPrefix(remainder, "─")
		for strings.HasPrefix(remainder, "┬─") {
			span++
			remainder = strings.TrimPrefix(remainder, "┬─")
		}
		if !strings.HasPrefix(remainder, "┐") {
			return record{}, false
		}
		remainder = strings.TrimPrefix(remainder, "┐")
	}
	padding := len(remainder) - len(strings.TrimLeft(remainder, " "))
	if padding < 2 || padding%2 != 0 {
		return record{}, false
	}
	name, ok := parseBranchLabel(remainder[padding:])
	if !ok {
		return record{}, false
	}
	return record{name: name, depth: depth, span: span, line: lineNumber}, true
}

func parseBranchLabel(label string) (string, bool) {
	seenCurrent := false
	seenNeedsRestack := false
	for strings.HasSuffix(label, ")") {
		index := strings.LastIndex(label, " (")
		if index < 0 {
			break
		}
		marker := label[index:]
		switch marker {
		case " (current)":
			if seenCurrent {
				return "", false
			}
			seenCurrent = true
		case " (needs restack)":
			if seenNeedsRestack {
				return "", false
			}
			seenNeedsRestack = true
		default:
			return "", false
		}
		label = label[:index]
	}
	if label == "" || strings.TrimSpace(label) != label || strings.ContainsAny(label, "\t\r\n") {
		return "", false
	}
	return label, true
}

func trimGraphPrefix(line string) (string, int) {
	depth := 0
	for {
		switch {
		case strings.HasPrefix(line, "│ "):
			line = strings.TrimPrefix(line, "│ ")
		case strings.HasPrefix(line, "  "):
			line = strings.TrimPrefix(line, "  ")
		default:
			return line, depth
		}
		depth++
	}
}

func drift(line int, content string) error {
	return fmt.Errorf("unsupported Graphite display grammar at line %d: %q", line, content)
}
