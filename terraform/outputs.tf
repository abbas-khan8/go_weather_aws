output "input_bucket" {
  value = aws_s3_bucket.input_bucket.id
}

output "output_bucket" {
  value = aws_s3_bucket.output_bucket.id
}

output "weather_lambda" {
  value = aws_lambda_function.weather_lambda.id
}