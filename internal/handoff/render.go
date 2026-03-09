package handoff

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/yusefmosiah/cagent/internal/core"
)

func RenderPrompt(targetAdapter string, packet core.HandoffPacket) string {
	var b strings.Builder

	fmt.Fprintf(&b, "You are continuing work from a prior %s session via a cagent handoff.\n\n", packet.Source.Adapter)
	if packet.Objective != "" {
		fmt.Fprintf(&b, "Objective:\n%s\n\n", packet.Objective)
	}
	if packet.Summary != "" {
		fmt.Fprintf(&b, "Summary:\n%s\n\n", packet.Summary)
	}
	if len(packet.Unresolved) > 0 {
		b.WriteString("Unresolved:\n")
		for _, item := range packet.Unresolved {
			fmt.Fprintf(&b, "- %s\n", item)
		}
		b.WriteString("\n")
	}
	if len(packet.ImportantFiles) > 0 {
		b.WriteString("Important files:\n")
		for _, path := range packet.ImportantFiles {
			fmt.Fprintf(&b, "- %s\n", path)
		}
		b.WriteString("\n")
	}
	if len(packet.Constraints) > 0 {
		b.WriteString("Constraints:\n")
		for _, item := range packet.Constraints {
			fmt.Fprintf(&b, "- %s\n", item)
		}
		b.WriteString("\n")
	}
	if len(packet.RecommendedNextSteps) > 0 {
		b.WriteString("Recommended next steps:\n")
		for _, item := range packet.RecommendedNextSteps {
			fmt.Fprintf(&b, "- %s\n", item)
		}
		b.WriteString("\n")
	}

	serialized, err := json.MarshalIndent(packet, "", "  ")
	if err == nil {
		fmt.Fprintf(&b, "Handoff packet for %s:\n```json\n%s\n```\n", targetAdapter, serialized)
	}

	return strings.TrimSpace(b.String())
}
