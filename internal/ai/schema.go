package ai

import "encoding/json"

// Pinned Chat Completions json_schema for meal vision (SPEC.md).
var mealSchema = json.RawMessage(`{
  "type": "object",
  "additionalProperties": false,
  "required": ["foods", "overall_confidence", "notes"],
  "properties": {
    "foods": {
      "type": "array",
      "maxItems": 20,
      "items": {
        "type": "object",
        "additionalProperties": false,
        "required": ["name", "grams", "kcal", "protein_g", "carbs_g", "fat_g", "confidence"],
        "properties": {
          "name": {"type": "string", "maxLength": 80},
          "grams": {"type": "number"},
          "kcal": {"type": "number"},
          "protein_g": {"type": "number"},
          "carbs_g": {"type": "number"},
          "fat_g": {"type": "number"},
          "confidence": {"type": "number"}
        }
      }
    },
    "overall_confidence": {"type": "number"},
    "notes": {"type": "string", "maxLength": 200}
  }
}`)
