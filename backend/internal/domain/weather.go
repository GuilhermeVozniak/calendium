package domain

// DayForecast is one day of weather forecast for one location, served on
// calendar surfaces as best-effort decoration (M2.8 Task 13). Sourced from
// Open-Meteo's daily forecast API via port.WeatherProvider.
type DayForecast struct {
	Date         string  `json:"date"` // YYYY-MM-DD in the requested zone
	Code         int     `json:"code"` // WMO weather interpretation code
	HighCelsius  float64 `json:"highCelsius"`
	LowCelsius   float64 `json:"lowCelsius"`
	PrecipChance int     `json:"precipChance"` // 0..100
}
