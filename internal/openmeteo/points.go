// Package openmeteo fetches hourly weather from the Open-Meteo forecast API for representative points
// of each bidding zone (ADR 0009, 0031).
package openmeteo

// Point is a representative location of a bidding zone: a load centre, or a wind or solar region.
type Point struct {
	Name      string
	Latitude  float64
	Longitude float64
}

// ZonePoints are the weather points per bidding zone name (internal/entsoe/zones.go), 3 to 5 each and
// at most 5 so that one request covers a zone. They are raw inputs: weighting them into a zone value
// is a dbt concern (ADR 0031). Coordinates are approximate city or region centres.
var ZonePoints = map[string][]Point{
	"AL":      {{"Tirana", 41.33, 19.82}, {"Durres", 41.32, 19.45}, {"Vlore", 40.47, 19.49}},
	"AT":      {{"Vienna", 48.21, 16.37}, {"Graz", 47.07, 15.44}, {"Linz", 48.31, 14.29}, {"Innsbruck", 47.27, 11.39}, {"Parndorf", 47.99, 16.86}},
	"BA":      {{"Sarajevo", 43.86, 18.41}, {"Banja Luka", 44.77, 17.19}, {"Mostar", 43.34, 17.81}},
	"BE":      {{"Brussels", 50.85, 4.35}, {"Antwerp", 51.22, 4.40}, {"Liege", 50.63, 5.57}, {"Ostend", 51.22, 2.93}, {"Belgian offshore", 51.60, 2.90}},
	"BG":      {{"Sofia", 42.70, 23.32}, {"Plovdiv", 42.14, 24.75}, {"Varna", 43.21, 27.91}, {"Kaliakra", 43.37, 28.46}},
	"CH":      {{"Zurich", 47.38, 8.54}, {"Geneva", 46.20, 6.14}, {"Basel", 47.56, 7.59}, {"Bern", 46.95, 7.45}, {"Valais", 46.23, 7.36}},
	"CY":      {{"Nicosia", 35.17, 33.36}, {"Limassol", 34.68, 33.04}, {"Larnaca", 34.92, 33.63}},
	"CZ":      {{"Prague", 50.08, 14.44}, {"Brno", 49.20, 16.61}, {"Ostrava", 49.82, 18.26}, {"Plzen", 49.75, 13.38}},
	"DE_LU":   {{"Berlin", 52.52, 13.40}, {"Munich", 48.14, 11.58}, {"Ruhr", 51.46, 7.01}, {"Schleswig-Holstein", 54.32, 9.50}, {"German North Sea", 54.40, 6.80}},
	"DK_1":    {{"Aarhus", 56.16, 10.20}, {"Esbjerg", 55.48, 8.45}, {"Aalborg", 57.05, 9.92}, {"Horns Rev", 55.53, 7.91}},
	"DK_2":    {{"Copenhagen", 55.68, 12.57}, {"Roskilde", 55.64, 12.08}, {"Lolland", 54.77, 11.50}},
	"EE":      {{"Tallinn", 59.44, 24.75}, {"Tartu", 58.38, 26.72}, {"Parnu", 58.39, 24.50}, {"Narva", 59.38, 28.19}},
	"ES":      {{"Madrid", 40.42, -3.70}, {"Barcelona", 41.39, 2.17}, {"Seville", 37.39, -5.98}, {"Zaragoza", 41.65, -0.88}, {"A Coruna", 43.36, -8.41}},
	"FI":      {{"Helsinki", 60.17, 24.94}, {"Tampere", 61.50, 23.79}, {"Oulu", 65.01, 25.47}, {"Vaasa", 63.10, 21.62}},
	"FR":      {{"Paris", 48.86, 2.35}, {"Lyon", 45.76, 4.84}, {"Marseille", 43.30, 5.37}, {"Nantes", 47.22, -1.55}, {"Lille", 50.63, 3.06}},
	"GR":      {{"Athens", 37.98, 23.73}, {"Thessaloniki", 40.64, 22.94}, {"Patras", 38.25, 21.73}, {"Evia", 38.46, 23.60}, {"Kozani", 40.30, 21.79}},
	"HR":      {{"Zagreb", 45.81, 15.98}, {"Split", 43.51, 16.44}, {"Rijeka", 45.33, 14.44}, {"Osijek", 45.55, 18.69}},
	"HU":      {{"Budapest", 47.50, 19.04}, {"Debrecen", 47.53, 21.63}, {"Szeged", 46.25, 20.15}, {"Gyor", 47.69, 17.63}},
	"IE_SEM":  {{"Dublin", 53.35, -6.26}, {"Belfast", 54.60, -5.93}, {"Cork", 51.90, -8.47}, {"Galway", 53.27, -9.05}, {"Mayo", 54.00, -9.40}},
	"IT_CALA": {{"Reggio Calabria", 38.11, 15.65}, {"Catanzaro", 38.91, 16.59}, {"Cosenza", 39.30, 16.25}},
	"IT_CNOR": {{"Florence", 43.77, 11.26}, {"Bologna", 44.49, 11.34}, {"Pisa", 43.72, 10.40}},
	"IT_CSUD": {{"Rome", 41.90, 12.50}, {"Naples", 40.85, 14.27}, {"Pescara", 42.46, 14.21}},
	"IT_NORD": {{"Milan", 45.46, 9.19}, {"Turin", 45.07, 7.69}, {"Venice", 45.44, 12.32}, {"Brescia", 45.54, 10.21}},
	"IT_SARD": {{"Cagliari", 39.22, 9.12}, {"Sassari", 40.73, 8.56}, {"Olbia", 40.92, 9.50}},
	"IT_SICI": {{"Palermo", 38.12, 13.36}, {"Catania", 37.50, 15.09}, {"Trapani", 38.02, 12.51}},
	"IT_SUD":  {{"Bari", 41.12, 16.87}, {"Foggia", 41.46, 15.55}, {"Taranto", 40.47, 17.24}, {"Potenza", 40.64, 15.80}},
	"LT":      {{"Vilnius", 54.69, 25.28}, {"Kaunas", 54.90, 23.90}, {"Klaipeda", 55.71, 21.14}},
	"LV":      {{"Riga", 56.95, 24.11}, {"Daugavpils", 55.87, 26.54}, {"Liepaja", 56.51, 21.01}},
	"ME":      {{"Podgorica", 42.44, 19.26}, {"Niksic", 42.78, 18.94}, {"Bar", 42.09, 19.10}},
	"MK":      {{"Skopje", 42.00, 21.43}, {"Bitola", 41.03, 21.33}, {"Stip", 41.74, 22.20}},
	"NL":      {{"Amsterdam", 52.37, 4.90}, {"Rotterdam", 51.92, 4.48}, {"Eindhoven", 51.44, 5.47}, {"Groningen", 53.22, 6.57}, {"Dutch North Sea", 52.60, 4.20}},
	"NO_1":    {{"Oslo", 59.91, 10.75}, {"Hamar", 60.79, 11.07}, {"Fredrikstad", 59.22, 10.93}},
	"NO_2":    {{"Kristiansand", 58.15, 8.00}, {"Stavanger", 58.97, 5.73}, {"Haugesund", 59.41, 5.27}},
	"NO_3":    {{"Trondheim", 63.43, 10.40}, {"Alesund", 62.47, 6.15}, {"Fosen", 63.80, 9.90}},
	"NO_4":    {{"Tromso", 69.65, 18.96}, {"Bodo", 67.28, 14.40}, {"Narvik", 68.44, 17.43}},
	"NO_5":    {{"Bergen", 60.39, 5.32}, {"Voss", 60.63, 6.42}, {"Forde", 61.45, 5.85}},
	"PL":      {{"Warsaw", 52.23, 21.01}, {"Krakow", 50.06, 19.94}, {"Katowice", 50.26, 19.02}, {"Gdansk", 54.35, 18.65}, {"Poznan", 52.41, 16.93}},
	"PT":      {{"Lisbon", 38.72, -9.14}, {"Porto", 41.15, -8.61}, {"Faro", 37.02, -7.93}, {"Viseu", 40.66, -7.91}},
	"RO":      {{"Bucharest", 44.43, 26.10}, {"Cluj-Napoca", 46.77, 23.60}, {"Constanta", 44.18, 28.63}, {"Iasi", 47.16, 27.59}, {"Timisoara", 45.75, 21.23}},
	"RS":      {{"Belgrade", 44.79, 20.45}, {"Novi Sad", 45.27, 19.83}, {"Nis", 43.32, 21.90}, {"Vrsac", 45.12, 21.30}},
	"SE_1":    {{"Lulea", 65.58, 22.15}, {"Kiruna", 67.86, 20.23}, {"Pitea", 65.32, 21.48}},
	"SE_2":    {{"Sundsvall", 62.39, 17.31}, {"Umea", 63.83, 20.26}, {"Ostersund", 63.18, 14.64}},
	"SE_3":    {{"Stockholm", 59.33, 18.07}, {"Gothenburg", 57.71, 11.97}, {"Uppsala", 59.86, 17.64}, {"Orebro", 59.27, 15.21}},
	"SE_4":    {{"Malmo", 55.60, 13.00}, {"Kalmar", 56.66, 16.36}, {"Karlskrona", 56.16, 15.59}},
	"SI":      {{"Ljubljana", 46.06, 14.51}, {"Maribor", 46.55, 15.65}, {"Koper", 45.55, 13.73}},
	"SK":      {{"Bratislava", 48.15, 17.11}, {"Kosice", 48.72, 21.26}, {"Zilina", 49.22, 18.74}},
	"XK":      {{"Pristina", 42.66, 21.17}, {"Prizren", 42.21, 20.74}, {"Peja", 42.66, 20.29}},
}
