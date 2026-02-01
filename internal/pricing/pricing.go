package pricing

import (
	"encoding/json"
	"fmt"
	"math"
)

// CalculateHumanizerPrice calculates the price for humanizer job based on word count.
// Rephrasy cost: ~₹0.80-1.60 per 100 words (assuming $0.01-0.02 per 100 words at ~₹80/$)
// We charge 1.5x markup, so: ₹1.20-2.40 per 100 words
// Using conservative estimate: ₹1.50 per 100 words (covers higher end + buffer)
func CalculateHumanizerPrice(wordCount int) int {
	if wordCount <= 0 {
		return 5000 // Minimum ₹50 for any job
	}

	// Rephrasy cost estimate: ₹1.00 per 100 words (conservative)
	rephrasyCostPer100Words := 100 // ₹1.00 in paise
	
	// Calculate Rephrasy cost
	rephrasyCost := (wordCount / 100) * rephrasyCostPer100Words
	if wordCount%100 > 0 {
		rephrasyCost += rephrasyCostPer100Words // Round up for partial 100s
	}
	
	// Apply 1.5x markup
	userPrice := int(math.Ceil(float64(rephrasyCost) * 1.5))
	
	// Minimum price: ₹50 (5000 paise)
	if userPrice < 5000 {
		userPrice = 5000
	}
	
	return userPrice
}

// EstimateWordCountFromPayload estimates word count from humanizer job payload.
// The payload should contain word_count if provided, otherwise we estimate from file size.
func EstimateWordCountFromPayload(payload []byte) (int, error) {
	var payloadMap map[string]interface{}
	if err := json.Unmarshal(payload, &payloadMap); err != nil {
		return 0, fmt.Errorf("unmarshal payload: %w", err)
	}
	
	// Check if word_count is provided
	if wordCount, ok := payloadMap["word_count"].(float64); ok {
		return int(wordCount), nil
	}
	
	// Estimate from file size (rough: 1KB ≈ 150 words for DOCX)
	// DOCX files are compressed, so we need to estimate
	// Average: ~200 words per KB of DOCX file
	if fileData, ok := payloadMap["file_data"].(string); ok {
		// Base64 encoded file: estimate original size
		// Base64 is ~33% larger, so divide by 1.33
		base64Size := len(fileData)
		estimatedOriginalSize := float64(base64Size) / 1.33
		
		// Rough estimate: 200 words per KB
		estimatedWords := int(estimatedOriginalSize / 1024 * 200)
		
		// Minimum estimate
		if estimatedWords < 100 {
			estimatedWords = 100
		}
		
		return estimatedWords, nil
	}
	
	// Default fallback: assume 1000 words
	return 1000, nil
}

