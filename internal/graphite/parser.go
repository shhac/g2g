package graphite

import (
	"bufio"
	"fmt"
	"regexp"
	"strings"
)

var (
	timeLine   = regexp.MustCompile(`^[│ ]*(?:[0-9]+ (?:seconds?|minutes?|hours?|days?|weeks?|months?|years?) ago|just now|now)$`)
	commitLine = regexp.MustCompile(`^[│ ]*[0-9a-f]{7,40} - .+$`)
	guideLine  = regexp.MustCompile(`^[│ ]*$`)
)

type graph struct {
	trunk string
	nodes map[string]node
}

type node struct {
	name   string
	parent string
}

type record struct {
	name  string
	depth int
	line  int
}

type event struct {
	fork   *record
	record *record
}

// parseLog accepts exactly the Graphite 1.8.6 output emitted by:
//
//	gt log --all --reverse --no-interactive
//
// The fixture grammar is intentionally small: each branch record is a graph
// heading followed by time, blank, abbreviated-commit, and guide lines. Fork
// markers must precede an increased graph depth. Any other syntax is drift.
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

	events, err := parseEvents(lines)
	if err != nil {
		return graph{}, err
	}
	return buildGraph(events)
}

func parseEvents(lines []string) ([]event, error) {
	var events []event
	for index := 0; index < len(lines); {
		if depth, ok := parseForkMarker(lines[index]); ok {
			fork := record{depth: depth, line: index + 1}
			events = append(events, event{fork: &fork})
			index++
			continue
		}
		record, err := parseRecord(lines, index)
		if err != nil {
			return nil, err
		}
		events = append(events, event{record: &record})
		index += 5
	}
	return events, nil
}

func parseRecord(lines []string, index int) (record, error) {
	if index+4 >= len(lines) {
		return record{}, drift(index+1, lines[index])
	}
	name, depth, ok := parseHeading(lines[index])
	if !ok || !timeLine.MatchString(lines[index+1]) || !guideLine.MatchString(lines[index+2]) || !commitLine.MatchString(lines[index+3]) || !guideLine.MatchString(lines[index+4]) {
		return record{}, drift(index+1, lines[index])
	}
	return record{name: name, depth: depth, line: index + 1}, nil
}

func buildGraph(events []event) (graph, error) {
	result := graph{nodes: make(map[string]node)}
	var previous *record
	lastAtDepth := make(map[int]string)
	markerDepth := -1

	for _, event := range events {
		if event.fork != nil {
			depth := event.fork.depth
			if previous == nil || markerDepth >= 0 {
				return graph{}, drift(event.fork.line, "fork marker")
			}
			if depth != previous.depth {
				return graph{}, drift(event.fork.line, "fork marker")
			}
			markerDepth = depth
			continue
		}
		current := *event.record
		if _, exists := result.nodes[current.name]; exists {
			return graph{}, fmt.Errorf("Graphite display repeats branch %q", current.name)
		}

		if previous == nil {
			if current.depth != 0 || markerDepth >= 0 {
				return graph{}, drift(current.line, current.name)
			}
			result.trunk = current.name
		} else {
			switch {
			case current.depth > previous.depth:
				if current.depth != previous.depth+1 || markerDepth != previous.depth {
					return graph{}, drift(current.line, current.name)
				}
				result.nodes[current.name] = node{name: current.name, parent: previous.name}
			case current.depth == previous.depth:
				if markerDepth >= 0 {
					return graph{}, drift(current.line, current.name)
				}
				result.nodes[current.name] = node{name: current.name, parent: previous.name}
			case current.depth < previous.depth:
				if markerDepth >= 0 {
					return graph{}, drift(current.line, current.name)
				}
				parent, exists := lastAtDepth[current.depth]
				if !exists {
					return graph{}, drift(current.line, current.name)
				}
				result.nodes[current.name] = node{name: current.name, parent: parent}
			}
		}
		markerDepth = -1
		if previous == nil {
			result.nodes[current.name] = node{name: current.name}
		}
		lastAtDepth[current.depth] = current.name
		previous = &current
	}
	if markerDepth >= 0 {
		return graph{}, fmt.Errorf("Graphite display ends after a fork marker")
	}
	return result, nil
}

func parseHeading(line string) (string, int, bool) {
	remainder, depth := trimGraphPrefix(line)
	var name string
	switch {
	case strings.HasPrefix(remainder, "◯ "):
		name = strings.TrimPrefix(remainder, "◯ ")
	case strings.HasPrefix(remainder, "◉ "):
		name = strings.TrimPrefix(remainder, "◉ ")
	default:
		return "", 0, false
	}
	name = strings.TrimSuffix(name, " (current)")
	if name == "" || strings.TrimSpace(name) != name || strings.ContainsAny(name, "\t\r\n") {
		return "", 0, false
	}
	return name, depth, true
}

func parseForkMarker(line string) (int, bool) {
	remainder, depth := trimGraphPrefix(line)
	return depth, remainder == "├──┐" || remainder == "└──┐"
}

func trimGraphPrefix(line string) (string, int) {
	depth := 0
	for {
		switch {
		case strings.HasPrefix(line, "│  "):
			line = strings.TrimPrefix(line, "│  ")
		case strings.HasPrefix(line, "   "):
			line = strings.TrimPrefix(line, "   ")
		default:
			return line, depth
		}
		depth++
	}
}

func drift(line int, content string) error {
	return fmt.Errorf("unsupported Graphite display grammar at line %d: %q", line, content)
}
