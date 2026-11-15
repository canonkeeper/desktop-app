// SPDX-License-Identifier: AGPL-3.0-only

package aisettings

import (
	"math"
	"math/big"
)

// Tokens are the tokens of a request as a cost counts them: in, the cached part of in, and out — the reasoning is in out.
type Tokens struct {
	Input, Cached, Output int64
}

// Sum is a sum in tokens (specs/2026-11-13-desk-consent.md CN1.2): in and out, the cache being a part of in.
func (tokens Tokens) Sum() int64 { return tokens.Input + tokens.Output }

// Add is the tokens of two requests.
func (tokens Tokens) Add(more Tokens) Tokens {
	return Tokens{Input: tokens.Input + more.Input, Cached: tokens.Cached + more.Cached, Output: tokens.Output + more.Output}
}

// Prices are what the author named for a million tokens in a currency: in, out and, if he named it, the cache.
type Prices struct {
	Currency Currency
	In, Out  Price
	Cache    *Price
}

// Prices are the prices of the settings when both the price in and the price out are named; without them there is no
// money, only tokens (CN1.2).
func (advanced Advanced) Prices() (Prices, bool) {
	if advanced.PriceIn == nil || advanced.PriceOut == nil {
		return Prices{}, false
	}
	currency := advanced.Currency
	if !currency.Valid() {
		currency = CurrencyUSD
	}
	return Prices{Currency: currency, In: *advanced.PriceIn, Out: *advanced.PriceOut, Cache: advanced.PriceCache}, true
}

// Cost is what a request cost in millionths of the currency (CN1.2): (in − cache)·price in + cache·price of the cache +
// out·price out, the cache at the price in when its own is not named, rounded up — a cost is never counted below what it
// was.
func (prices Prices) Cost(tokens Tokens) int64 {
	cached := min(max(tokens.Cached, 0), max(tokens.Input, 0))
	cache := prices.In
	if prices.Cache != nil {
		cache = *prices.Cache
	}
	// A price is in ten-thousandths of a unit for a million tokens: tokens·price/10 000 is millionths of a unit.
	sum := new(big.Int).Mul(big.NewInt(max(tokens.Input, 0)-cached), big.NewInt(int64(prices.In)))
	sum.Add(sum, new(big.Int).Mul(big.NewInt(cached), big.NewInt(int64(cache))))
	sum.Add(sum, new(big.Int).Mul(big.NewInt(max(tokens.Output, 0)), big.NewInt(int64(prices.Out))))
	quotient, remainder := new(big.Int).QuoRem(sum, big.NewInt(priceScale), new(big.Int))
	if remainder.Sign() > 0 {
		quotient.Add(quotient, big.NewInt(1))
	}
	if !quotient.IsInt64() {
		return math.MaxInt64
	}
	return quotient.Int64()
}

// FallbackCalibration is the input tokens of 1 000 characters of Russian text when no check of the connection measured
// them for the model of the reading: the top of the measurement (CN1.4).
const FallbackCalibration = 420.0

// bytesPerCharacter is how many bytes of a request a character of its text takes, on average: a Russian letter takes two,
// a space, a figure and the JSON around one.
const bytesPerCharacter = 1.5

// Unseen is a request the provider sent no usage for, counted by an estimate (CN3.3): the input by the size of the request
// and the calibration, the output by the characters that went by — of a request canceled, what went by before.
func Unseen(requestBytes, outputCharacters int64, calibration float64) Tokens {
	if calibration <= 0 {
		calibration = FallbackCalibration
	}
	return Tokens{
		Input:  int64(math.Ceil(float64(max(requestBytes, 0)) / bytesPerCharacter * calibration / 1000)),
		Output: int64(math.Ceil(float64(max(outputCharacters, 0)) * calibration / 1000)),
	}
}
