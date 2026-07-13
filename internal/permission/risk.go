package permission

import "strings"

type RiskLevel int

const (
	Safe RiskLevel = iota
	Low
	High
	Critical
)

func (r RiskLevel) String() string {
	switch r {
	case Safe:
		return "Safe"
	case Low:
		return "Low"
	case High:
		return "High"
	case Critical:
		return "Critical"
	default:
		return "Unknown"
	}
}

func ParseRiskLevel(value string) (RiskLevel, bool) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "safe":
		return Safe, true
	case "low":
		return Low, true
	case "high":
		return High, true
	case "critical":
		return Critical, true
	default:
		return Safe, false
	}
}

func MaxRisk(a, b RiskLevel) RiskLevel {
	if a > b {
		return a
	}
	return b
}
