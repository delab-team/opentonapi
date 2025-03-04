package rates

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"

	"github.com/labstack/gommon/log"
	"github.com/tonkeeper/opentonapi/pkg/references"
	"github.com/tonkeeper/tongo"
	"github.com/tonkeeper/tongo/tep64"
)

var errorsCounter = promauto.NewCounterVec(prometheus.CounterOpts{
	Name: "rates_getter_errors_total",
}, []string{"source"})

type storage interface {
	GetJettonMasterMetadata(ctx context.Context, master tongo.AccountID) (tep64.Metadata, error)
}

func (m *Mock) GetCurrentRates() (map[string]float64, error) {
	rates := make(map[string]float64)
	const tonstakers string = "tonstakers"

	marketsPrice := m.GetCurrentMarketsTonPrice()
	medianTonPriceToUsd, err := getMedianTonPrice(marketsPrice)
	if err != nil {
		return rates, err
	}

	fiatPrices := getFiatPrices()
	pools := getPools(medianTonPriceToUsd, m.Storage)

	for attempt := 0; attempt < 3; attempt++ {
		if tonstakersJetton, tonstakersPrice, err := getTonstakersPrice(references.TonstakersAccountPool); err == nil {
			pools[tonstakersJetton] = tonstakersPrice
			break
		}
		errorsCounter.WithLabelValues(tonstakers).Inc()
		time.Sleep(time.Second * 3)
	}

	// All data is displayed to the ratio to TON
	// For example: 1 Jetton = ... TON, 1 USD = ... TON
	rates["TON"] = 1
	for currency, price := range fiatPrices {
		if price != 0 {
			rates[currency] = 1 / (price * medianTonPriceToUsd)
		}
	}
	for token, coinsCount := range pools {
		rates[token.ToRaw()] = coinsCount
	}

	return rates, nil
}

func getMedianTonPrice(marketsPrice []Market) (float64, error) {
	var prices []float64
	for _, market := range marketsPrice {
		prices = append(prices, market.UsdPrice)
	}
	sort.Float64s(prices)

	length := len(prices)
	if length%2 == 0 { // if the length of the array is even, take the average of the two middle elements
		middle1 := prices[length/2-1]
		middle2 := prices[length/2]
		return (middle1 + middle2) / 2, nil
	}

	// if the length of the array is odd, return the middle element.
	return prices[length/2], nil
}

// getTonstakersPrice is used to retrieve the price and token address of an account on the Tonstakers pool.
// We are using the TonApi, because the standard liteserver executor is incapable of invoking methods on the account
func getTonstakersPrice(pool tongo.AccountID) (tongo.AccountID, float64, error) {
	resp, err := http.Get(fmt.Sprintf("https://tonapi.io/v2/blockchain/accounts/%v/methods/get_pool_full_data", pool.ToRaw()))
	if err != nil {
		log.Errorf("[getTonstakersPrice] can't load price")
		return tongo.AccountID{}, 0, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return tongo.AccountID{}, 0, fmt.Errorf("bad status code: %v", resp.StatusCode)
	}
	var respBody struct {
		Success bool `json:"success"`
		Decoded struct {
			JettonMinter    string `json:"jetton_minter"`
			ProjectBalance  int64  `json:"projected_balance"`
			ProjectedSupply int64  `json:"projected_supply"`
		}
	}
	if err = json.NewDecoder(resp.Body).Decode(&respBody); err != nil {
		log.Errorf("[getTonstakersPrice] failed to decode response: %v", err)
		return tongo.AccountID{}, 0, err
	}

	if !respBody.Success {
		return tongo.AccountID{}, 0, fmt.Errorf("failed success")
	}
	if respBody.Decoded.ProjectBalance == 0 || respBody.Decoded.ProjectedSupply == 0 {
		return tongo.AccountID{}, 0, fmt.Errorf("empty balance")
	}
	accountJetton, err := tongo.ParseAddress(respBody.Decoded.JettonMinter)
	if err != nil {
		return tongo.AccountID{}, 0, err
	}
	price := float64(respBody.Decoded.ProjectBalance) / float64(respBody.Decoded.ProjectedSupply)

	return accountJetton.ID, price, nil
}
