// Auriga modifications: trimmed relay core/providers; see third_party/bifrost/UPSTREAM.md.

package keyselectors

import (
	"math"
	"math/rand"

	"github.com/maximhq/bifrost/core/schemas"
)

func WeightedRandom(ctx *schemas.BifrostContext, keys []schemas.Key, providerKey schemas.ModelProvider, model string) (schemas.Key, error) {

	totalWeight := 0.0
	for _, key := range keys {
		totalWeight += usableWeight(key.Weight)
	}

	if totalWeight == 0 {
		return keys[rand.Intn(len(keys))], nil
	}

	scale := 1.0
	if math.IsInf(totalWeight, 1) {
		maxWeight := 0.0
		for _, key := range keys {
			maxWeight = max(maxWeight, usableWeight(key.Weight))
		}
		scale = 1 / maxWeight
		totalWeight = 0
		for _, key := range keys {
			totalWeight += usableWeight(key.Weight) * scale
		}
	}

	randomValue := rand.Float64() * totalWeight

	currentWeight := 0.0
	for _, key := range keys {
		currentWeight += usableWeight(key.Weight) * scale
		if randomValue < currentWeight {
			return key, nil
		}
	}

	return keys[0], nil
}

func usableWeight(weight float64) float64 {
	if weight < 0 || math.IsNaN(weight) || math.IsInf(weight, 0) {
		return 0
	}
	return weight
}
