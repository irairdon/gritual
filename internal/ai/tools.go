package ai

import "encoding/json"

const (
	ToolLogMeal               = "log_meal"
	ToolLogActivity           = "log_activity"
	ToolLogWeight             = "log_weight"
	ToolCreateRitual          = "create_ritual"
	ToolGetTodayMacros        = "get_today_macros"
	ToolGetChallengeStandings = "get_challenge_standings"
)

// ToolNames is the unified chat + MCP tool list. Aliases like create_goal are not implemented.
var ToolNames = []string{
	ToolCreateRitual,
	ToolGetTodayMacros,
	ToolGetChallengeStandings,
	ToolLogMeal,
	ToolLogActivity,
	ToolLogWeight,
}

type ToolSpec struct {
	Name        string
	Description string
	Parameters  json.RawMessage
}

func ToolSpecs() []ToolSpec {
	return []ToolSpec{
		{
			Name:        ToolLogMeal,
			Description: "Log a confirmed meal for the current user. Do not pass user_id or visibility.",
			Parameters:  json.RawMessage(`{"type":"object","additionalProperties":false,"required":["items"],"properties":{"items":{"type":"array","items":{"type":"object","additionalProperties":false,"required":["name","grams","kcal","protein_g","carbs_g","fat_g"],"properties":{"name":{"type":"string"},"grams":{"type":"number"},"kcal":{"type":"number"},"protein_g":{"type":"number"},"carbs_g":{"type":"number"},"fat_g":{"type":"number"}}}},"notes":{"type":"string"},"logged_at":{"type":"string"}}}`),
		},
		{
			Name:        ToolLogActivity,
			Description: "Log a workout, habit, fishing trip, or custom value for the current user.",
			Parameters:  json.RawMessage(`{"type":"object","additionalProperties":false,"required":["type"],"properties":{"type":{"type":"string","enum":["workout","habit","fishing","custom"]},"title":{"type":"string"},"sets":{"type":"array","items":{"type":"object","additionalProperties":false,"properties":{"exercise":{"type":"string"},"reps":{"type":"integer"},"weight_kg":{"type":"number"},"rpe":{"type":"number"},"ordinal":{"type":"integer"}}}},"status":{"type":"string","enum":["done","skip"]},"water_body":{"type":"string"},"lat":{"type":"number"},"lng":{"type":"number"},"catches":{"type":"array","items":{"type":"object","additionalProperties":false,"properties":{"species":{"type":"string"},"count":{"type":"integer"},"length_cm":{"type":"number"},"released":{"type":"boolean"}}}},"value":{"type":"number"},"unit":{"type":"string"},"notes":{"type":"string"},"logged_at":{"type":"string"}}}`),
		},
		{
			Name:        ToolLogWeight,
			Description: "Log body weight. Pass exactly one of kg or lb.",
			Parameters:  json.RawMessage(`{"type":"object","additionalProperties":false,"properties":{"kg":{"type":"number"},"lb":{"type":"number"},"logged_at":{"type":"string"},"notes":{"type":"string"}}}`),
		},
		{
			Name:        ToolCreateRitual,
			Description: "Create a personal ritual for the current user (not a circle ritual).",
			Parameters:  json.RawMessage(`{"type":"object","additionalProperties":false,"required":["type","title"],"properties":{"type":{"type":"string","enum":["weight","workout","habit","fishing","meal","custom"]},"title":{"type":"string"},"target_value":{"type":"number"},"direction":{"type":"string","enum":["at_least","at_most","hit"]},"period":{"type":"string","enum":["none","daily","weekly","season","date_range"]}}}`),
		},
		{
			Name:        ToolGetTodayMacros,
			Description: "Sum confirmed meals for the current user in their timezone today.",
			Parameters:  json.RawMessage(`{"type":"object","additionalProperties":false,"properties":{}}`),
		},
		{
			Name:        ToolGetChallengeStandings,
			Description: "Get standings for a challenge the current user has joined.",
			Parameters:  json.RawMessage(`{"type":"object","additionalProperties":false,"required":["challenge_id"],"properties":{"challenge_id":{"type":"string"}}}`),
		},
	}
}

func KnownTool(name string) bool {
	for _, n := range ToolNames {
		if n == name {
			return true
		}
	}
	return false
}
