package version

import (
	"regexp"
	"strconv"
	"strings"
)

var pattern = regexp.MustCompile(`^v?([0-9]+)\.([0-9]+)\.([0-9]+)(?:[.-]([0-9A-Za-z.-]+))?$`)

type parsed struct {
	numbers    [3]uint64
	prerelease []string
}

func Normalize(value string) (string, bool) {
	value = strings.TrimSpace(value)
	if _, ok := parse(value); !ok {
		return "", false
	}
	return strings.TrimPrefix(value, "v"), true
}

func Compare(left, right string) (int, bool) {
	leftVersion, leftOK := parse(strings.TrimSpace(left))
	rightVersion, rightOK := parse(strings.TrimSpace(right))
	if !leftOK || !rightOK {
		return 0, false
	}
	for index := range leftVersion.numbers {
		if leftVersion.numbers[index] < rightVersion.numbers[index] {
			return -1, true
		}
		if leftVersion.numbers[index] > rightVersion.numbers[index] {
			return 1, true
		}
	}
	return comparePrerelease(leftVersion.prerelease, rightVersion.prerelease), true
}

func parse(value string) (parsed, bool) {
	matches := pattern.FindStringSubmatch(value)
	if matches == nil {
		return parsed{}, false
	}
	var result parsed
	for index := range result.numbers {
		number, err := strconv.ParseUint(matches[index+1], 10, 64)
		if err != nil {
			return parsed{}, false
		}
		result.numbers[index] = number
	}
	if matches[4] != "" {
		result.prerelease = strings.Split(matches[4], ".")
	}
	return result, true
}

func comparePrerelease(left, right []string) int {
	if len(left) == 0 && len(right) == 0 {
		return 0
	}
	if len(left) == 0 {
		return 1
	}
	if len(right) == 0 {
		return -1
	}
	limit := min(len(left), len(right))
	for index := 0; index < limit; index++ {
		leftNumber, leftNumeric := numericIdentifier(left[index])
		rightNumber, rightNumeric := numericIdentifier(right[index])
		switch {
		case leftNumeric && rightNumeric && leftNumber < rightNumber:
			return -1
		case leftNumeric && rightNumeric && leftNumber > rightNumber:
			return 1
		case leftNumeric && !rightNumeric:
			return -1
		case !leftNumeric && rightNumeric:
			return 1
		case left[index] < right[index]:
			return -1
		case left[index] > right[index]:
			return 1
		}
	}
	if len(left) < len(right) {
		return -1
	}
	if len(left) > len(right) {
		return 1
	}
	return 0
}

func numericIdentifier(value string) (uint64, bool) {
	if value == "" {
		return 0, false
	}
	for _, character := range value {
		if character < '0' || character > '9' {
			return 0, false
		}
	}
	number, err := strconv.ParseUint(value, 10, 64)
	return number, err == nil
}
