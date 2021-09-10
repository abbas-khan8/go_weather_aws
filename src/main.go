package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-lambda-go/lambda"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/jszwec/csvutil"
)

// S3PutObjectAPI defines the interface for the PutObject function.
type S3PutObjectAPI interface {
	PutObject(ctx context.Context,
		params *s3.PutObjectInput,
		optFns ...func(*s3.Options)) (*s3.PutObjectOutput, error)
}

// S3DeleteObjectAPI defines the interface for the DeleteObject function.
type S3DeleteObjectAPI interface {
	DeleteObject(ctx context.Context,
		params *s3.DeleteObjectInput,
		optFns ...func(*s3.Options)) (*s3.DeleteObjectOutput, error)
}

// Response defines the interface for the lambda response code and a message
type Response struct {
	StatusCode    string `json:"statusCode"`
	StatusMessage string `json:"statusMessage"`
}

// Weather defines the interface for the json object returned from the api
type Weather struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
	Main struct {
		Temp      float32 `json:"temp"`
		FeelsLike float32 `json:"feels_like"`
		TempMin   float32 `json:"temp_min"`
		TempMax   float32 `json:"temp_max"`
		Pressure  int     `json:"pressure"`
		Humidity  int     `json:"humidity"`
	} `json:"main"`
	Wind struct {
		Speed   float32 `json:"speed"`
		Degrees int     `json:"deg"`
	} `json:"wind"`
}

// TemperatureOutput defines the interface for the csv temperature data
type TemperatureOutput struct {
	City        string  `csv:"City"`
	Temperature float64 `csv:"Temperature"`
}

// WindOutput defines the interface for the csv wind speed data
type WindOutput struct {
	City      string  `csv:"City"`
	WindSpeed float64 `csv:"Wind Speed"`
}

var (
	s3Client  *s3.Client
	uploadKey string
)

func main() {
	lambda.Start(handler)
}

func handler(ctx context.Context, event events.S3Event) (Response, error) {
	// Load the Shared AWS Configuration (~/.aws/config)
	cfg, err := config.LoadDefaultConfig(context.TODO())
	if err != nil {
		log.Fatal(err)
		return Response{StatusCode: "400", StatusMessage: fmt.Sprint("", err)}, err
	}

	// Create an Amazon S3 service client
	s3Client = s3.NewFromConfig(cfg)

	uploadKey = event.Records[0].S3.Object.Key

	err = processWeather()

	if err != nil {
		return Response{StatusCode: "400", StatusMessage: fmt.Sprint("", err)}, err
	}

	return Response{StatusCode: "200", StatusMessage: "Success"}, nil
}

// processWeather calls relevant functions to process weather data
// Output:
//     If success returns nil, otherwise an error
func processWeather() error {
	cities := make([]string, 0)

	if err := extractCities(&cities); err != nil {
		return err
	}

	weatherList := make([]Weather, len(cities))

	err := populateWeatherList(cities, &weatherList)

	if err != nil {
		return err
	}

	temperatureList, windList := extractWeatherInfo(weatherList)

	err = writeTemperatures(temperatureList)
	if err != nil {
		return err
	}

	err = writeWindSpeed(windList)
	if err != nil {
		return err
	}

	err = runCleanup()
	if err != nil {
		return err
	}

	return nil
}

// extractCities opens uploaded file, extracts city names and populates list of string pointers
// Inputs:
//	   cities: list of city name strings pointers to populate
// Output:
//     If success returns nil, otherwise an error
func extractCities(cities *[]string) error {
	response, err := s3Client.GetObject(context.TODO(), &s3.GetObjectInput{
		Bucket: aws.String(os.Getenv("INPUT_BUCKET")),
		Key:    aws.String(uploadKey),
	})
	if err != nil {
		return fmt.Errorf("failed to extract data from file! %s", err)
	}

	defer response.Body.Close()

	// Load body of response into scanner
	scanner := bufio.NewScanner(response.Body)
	scanner.Split(SplitAt(","))

	for scanner.Scan() {
		city := strings.Join(strings.Fields(scanner.Text()), "")
		*cities = append(*cities, city)
	}

	return nil
}

// Custom optimised function to pass to Scanner which splits at specified token
// https://stackoverflow.com/questions/33068644/how-a-scanner-can-be-implemented-with-a-custom-split
func SplitAt(substring string) func(data []byte, atEOF bool) (advance int, token []byte, err error) {
	searchBytes := []byte(substring)
	searchLen := len(searchBytes)
	return func(data []byte, atEOF bool) (advance int, token []byte, err error) {
		dataLen := len(data)

		// Return nothing if at end of file and no data passed
		if atEOF && dataLen == 0 {
			return 0, nil, nil
		}

		// Find next separator and return token
		if i := bytes.Index(data, searchBytes); i >= 0 {
			return i + searchLen, data[0:i], nil
		}

		// If we're at EOF, we have a final, non-terminated line. Return it.
		if atEOF {
			return dataLen, data, nil
		}

		// Request more data.
		return 0, nil, nil
	}
}

// populateWeatherList calls api and populates list of Weather pointers based on city names
// Inputs:
//	   cities: list of city name strings
//     weatherList: list of Weather struct pointers
// Output:
//     If success returns nil, otherwise an error
func populateWeatherList(cities []string, weatherList *[]Weather) error {
	weatherClient := http.Client{
		Timeout: time.Second * 2,
	}

	units := "metric"
	apiKey := "bae5f0a6b8df97353331c09833748800"

	for _, c := range cities {
		url := "https://api.openweathermap.org/data/2.5/weather"
		params := fmt.Sprintf("?q=%s&units=%s&appid=%s", c, units, apiKey)
		endpoint := url + params

		request, err := http.NewRequest(http.MethodGet, endpoint, nil)

		if err != nil {
			return fmt.Errorf("request failed! %s", err)
		}

		response, err := weatherClient.Do(request)

		if err != nil {
			return fmt.Errorf("response failed! %s", err)
		}

		if response.Body != nil {
			defer response.Body.Close()
		}

		body, err := ioutil.ReadAll(response.Body)

		if err != nil {
			return fmt.Errorf("failed to read response body! %s", err)
		}

		cityWeather := Weather{}
		jsonErr := json.Unmarshal(body, &cityWeather)

		if jsonErr != nil {
			return fmt.Errorf("failed to load JSON into Struct! %s", err)
		}

		*weatherList = append(*weatherList, cityWeather)
	}

	return nil
}

// extractWeatherInfo reads a list of weather information and splits into seperate slices for temperature and wind speed
// Inputs:
//     weatherList: list of Weather structs to split
// Output:
//     []TemperatureOutput: list of 3 cities with highest temperatures
//	   []WindOutput: list of 3 cities with highest wind speeds
func extractWeatherInfo(weatherList []Weather) ([]TemperatureOutput, []WindOutput) {
	temperatureList := make([]TemperatureOutput, len(weatherList))
	windList := make([]WindOutput, len(weatherList))

	for i, city := range weatherList {
		name := city.Name

		temperatureList[i] = TemperatureOutput{City: name, Temperature: float64(city.Main.Temp)}
		windList[i] = WindOutput{City: name, WindSpeed: float64(city.Wind.Speed)}
	}

	sort.SliceStable(temperatureList, func(i, j int) bool {
		return temperatureList[i].Temperature > temperatureList[j].Temperature
	})

	sort.SliceStable(windList, func(i, j int) bool {
		return windList[i].WindSpeed > windList[j].WindSpeed
	})

	return temperatureList[:3], windList[:3]
}

// writeTemperatures marshals list of cities and temperatures into a csv string
//	   and inserts file into s3 ouput bucket
// Inputs:
//     temperatureList: list of TemperatureOutput structs to marshal
// Output:
//     If success returns nil, otherwise an error
func writeTemperatures(temperatureList []TemperatureOutput) error {
	body, err := csvutil.Marshal(temperatureList)

	if err != nil {
		return fmt.Errorf("failed to marshal csv from temperature list! %s", err)
	}
	fmt.Println(string(body))

	key := "highest_temperatures.csv"
	params := &s3.PutObjectInput{
		Bucket: aws.String(os.Getenv("OUTPUT_BUCKET")),
		Key:    aws.String(key),
		Body:   bytes.NewReader([]byte(body)),
	}

	_, err = PutObject(context.TODO(), s3Client, params)
	if err != nil {
		return fmt.Errorf("error uploading temperature file! %s", err)
	}

	return nil
}

// writeWindSpeed marshals list of cities and wind speeds into a csv string
//		and inserts file into s3 ouput bucket
// Inputs:
//     windList: list of WindOutput structs to marshal
// Output:
//     If success returns nil, otherwise an error
func writeWindSpeed(windList []WindOutput) error {
	body, err := csvutil.Marshal(windList)

	if err != nil {
		return fmt.Errorf("failed to marshal csv from wind speed list! %s", err)
	}
	fmt.Println(string(body))

	key := "highest_wind.csv"
	params := &s3.PutObjectInput{
		Bucket: aws.String(os.Getenv("OUTPUT_BUCKET")),
		Key:    aws.String(key),
		Body:   bytes.NewReader([]byte(body)),
	}

	_, err = PutObject(context.TODO(), s3Client, params)
	if err != nil {
		return fmt.Errorf("error uploading wind speed file! %s", err)
	}

	return nil
}

// runCleanup deletes the upload file object from s3 input bucket
// Output:
//     If success returns nil, otherwise an error
func runCleanup() error {
	params := &s3.DeleteObjectInput{
		Bucket: aws.String(os.Getenv("INPUT_BUCKET")),
		Key:    aws.String(uploadKey),
	}

	_, err := DeleteObject(context.TODO(), s3Client, params)
	if err != nil {
		return fmt.Errorf("error removing upload file! %s", err)
	}

	return nil
}

// PutFile uploads a file to an Amazon Simple Storage Service (Amazon S3) bucket
// Inputs:
//     c is the context of the method call, which includes the AWS Region
//     api is the interface that defines the method call
//     input defines the input arguments to the service call.
// Output:
//     If success, a PutObjectOutput object containing the result of the service call and nil
//     Otherwise, nil and an error from the call to PutObject
func PutObject(c context.Context, api S3PutObjectAPI, input *s3.PutObjectInput) (*s3.PutObjectOutput, error) {
	return api.PutObject(c, input)
}

// DeleteItem deletes an object from an Amazon Simple Storage Service (Amazon S3) bucket
// Inputs:
//     c is the context of the method call, which includes the AWS Region
//     api is the interface that defines the method call
//     input defines the input arguments to the service call.
// Output:
//     If success, a DeleteObjectOutput object containing the result of the service call and nil
//     Otherwise, an error from the call to DeleteObject
func DeleteObject(c context.Context, api S3DeleteObjectAPI, input *s3.DeleteObjectInput) (*s3.DeleteObjectOutput, error) {
	return api.DeleteObject(c, input)
}
