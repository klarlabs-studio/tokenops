package focus

import (
	"fmt"
	"math"
	"time"
)

// Blend is one day's usage of one provider's models taken together: a
// row of the team plane's organisation dataset, which names no model
// (ADR 0012 §11).
type Blend struct {
	// Day is the UTC day it happened on.
	Day      time.Time
	Provider string
	Requests int64
	Tokens   int64
	// ListUSD is the usage at API list prices; EstimatedUSD what
	// TokenOps estimates was billed, nothing for usage a plan covered.
	ListUSD, EstimatedUSD float64
	// Unpriced is true when some of the usage has no rate card, so
	// ListUSD understates it.
	Unpriced bool
	// Tags are the row's labels, x_-prefixed: the team plane's, not an
	// operator's allocation tags.
	Tags map[string]string
}

// Blended writes b as one FOCUS 1.4 usage row: the provider's tokens,
// priced at the blend of the models used. Its unit prices are what a
// million of the provider's tokens cost on average that day, not a price
// any vendor publishes; the description says so. As in the client's own
// export, BilledCost and EffectiveCost are 0 and the estimate is in
// x_EstimatedCost; ContractedCost is the estimate too, so ListCost
// against it is what plans saved.
func Blended(b Blend) Row {
	day := b.Day.UTC()
	tokens := b.Tokens
	vendor := vendorName(b.Provider)
	r := base(b.Provider, day, day.Add(24*time.Hour))
	r.HostProviderName = r.ProviderName
	r.ChargeCategory = CategoryUsage
	r.ChargeFrequency = FrequencyUsageBased
	r.BillingCurrency = "USD"
	r.BillingAccountID = b.Provider + "/organisation"
	r.BillingAccountName = vendor + ", across the organisation"
	r.ServiceName = vendor + " models"
	r.ConsumedQuantity = &tokens
	r.ConsumedUnit = unitTokens
	r.pricingQuantity = millions(tokens)
	r.PricingUnit = unitMillionTokens
	r.SkuID = b.Provider + "/models"
	r.SkuPriceID = r.SkuID + "/tokens"
	// The unit prices are rounded as they are written, and the costs
	// derived from them, so a cost is exactly its price times the
	// quantity.
	list, contracted := blendPrice(b.ListUSD, tokens), blendPrice(b.EstimatedUSD, tokens)
	r.ListUnitPrice, r.ContractedUnitPrice = &list, &contracted
	r.ListCost = list * float64(tokens) / 1e6
	r.ContractedCost = contracted * float64(tokens) / 1e6
	r.EstimatedCost = b.EstimatedUSD
	r.ChargeDescription = fmt.Sprintf("Tokens of %s to %s models, across the organisation; the unit prices are "+
		"the day's blend of the models used, not a published price; x_EstimatedCost is TokenOps' estimate, "+
		"BilledCost is left to the vendor's invoice", requests(b.Requests), vendor)
	if b.Unpriced {
		r.ChargeDescription += "; no API rate is known for some of it, so ListCost understates it"
	}
	r.Tags = tagsJSON(b.Tags)
	return r
}

// blendPrice is usd per million of tokens, rounded as decimal writes it;
// 0 with no tokens.
func blendPrice(usd float64, tokens int64) float64 {
	if tokens <= 0 {
		return 0
	}
	return math.Round(usd*1e6/float64(tokens)*1e10) / 1e10
}
