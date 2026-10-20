package timestral

import (
	"context"
	"math"
	"testing"
)

// BenchmarkForecast measures one forecast (288 steps ahead, with symmetric averaging) for a
// short and a long context: go test -bench Forecast ./pkg/timestral/
func BenchmarkForecast(b *testing.B) {
	m := testModel(b)
	for _, ctx := range []int{1500, 8000} {
		series := make([]float64, ctx)
		for i := range series {
			series[i] = 50 + 20*math.Sin(float64(i)/40)
		}
		b.Run("context="+itoa(ctx), func(b *testing.B) {
			for range b.N {
				if _, err := m.Forecast(context.Background(), Input{Context: [][][]float64{{series}}, PredictionLength: 288}); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for ; n > 0; n /= 10 {
		b = append([]byte{byte('0' + n%10)}, b...)
	}
	return string(b)
}
