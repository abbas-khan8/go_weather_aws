terraform {
  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "~> 3.27"
    }
  }

  required_version = ">= 0.14.9"
}

provider "aws" {
  profile = "default"
  region  = "eu-west-2"
}

locals {
  input_bucket  = "weather-input-bucket"
  output_bucket = "weather-output-bucket"
  lambda_bin    = "main"
  output_path   = "../target/${local.lambda_bin}.zip"
  lambda_name   = "go-weather-lambda"
}

//***Buckets***//
resource "aws_s3_bucket" "input_bucket" {
  bucket = "weather-input-bucket"
  acl    = "private"

  tags = {
    Name = "S3 Input Bucket for weather files"
  }
}

resource "aws_s3_bucket" "output_bucket" {
  bucket = "weather-output-bucket"
  acl    = "private"

  tags = {
    Name = "S3 Output Bucket for weather files"
  }
}


//***Weather Lambda Role***//
resource "aws_iam_role" "weather_lambda_role" {
  name               = "iam-role-weather-lambda"
  description        = "Execution Role for Weather Lambda."
  assume_role_policy = <<-EOF
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Action": "sts:AssumeRole",
      "Principal": {
        "Service": "lambda.amazonaws.com"
      },
      "Effect": "Allow"
    }
  ]
}
EOF

  tags = {
    name = "Lambda role for Weather Lambda"
  }
}

resource "aws_iam_policy" "weather_lambda_policy" {
  name        = "iam-policy-weather-lambda"
  description = "Policy for Weather Lambda."

  policy = <<-EOF
{
    "Version": "2012-10-17",
    "Statement": [
        {
            "Effect": "Allow",
            "Action": [
                "logs:CreateLogGroup",
                "logs:CreateLogStream",
                "logs:PutLogEvents"
            ],
            "Resource": "arn:aws:logs:*:*:*"
        },
        {
            "Effect": "Allow",
            "Action": [
                "s3:GetObject",
                "s3:PutObject",
                "s3:DeleteObject"
            ],
            "Resource": [
              "${aws_s3_bucket.input_bucket.arn}/*",
              "${aws_s3_bucket.output_bucket.arn}/*"
            ]
        },
        {
            "Effect": "Allow",
            "Action": [
                "s3:ListBucket"
            ],
            "Resource":"${aws_s3_bucket.input_bucket.arn}"
        }
    ]
  }
  EOF

}

resource "aws_iam_role_policy_attachment" "weather_lambda_policy_attachment" {
  role       = aws_iam_role.weather_lambda_role.name
  policy_arn = aws_iam_policy.weather_lambda_policy.arn
}


//***Weather Lambda Resource***//
resource "aws_cloudwatch_log_group" "weather_lambda_log" {
  name = "/aws/lambda/${local.lambda_name}_log"
}

resource "aws_lambda_function" "weather_lambda" {
  function_name    = local.lambda_name
  handler          = local.lambda_bin
  runtime          = "go1.x"
  role             = aws_iam_role.weather_lambda_role.arn
  filename         = local.output_path
  source_code_hash = filebase64sha256(local.output_path)
  memory_size      = 128
  timeout          = 10

  environment {
    variables = {
      INPUT_BUCKET  = local.input_bucket
      OUTPUT_BUCKET = local.output_bucket
    }
  }

  depends_on = [
    aws_cloudwatch_log_group.weather_lambda_log
  ]
}


//***Weather Lambda Trigger***//
resource "aws_lambda_permission" "allow_bucket" {
  statement_id  = "AllowExecutionFromS3Bucket"
  action        = "lambda:InvokeFunction"
  function_name = aws_lambda_function.weather_lambda.arn
  principal     = "s3.amazonaws.com"
  source_arn    = aws_s3_bucket.input_bucket.arn
}

resource "aws_s3_bucket_notification" "input_bucket_notification" {
  bucket = aws_s3_bucket.input_bucket.id

  lambda_function {
    lambda_function_arn = aws_lambda_function.weather_lambda.arn
    events              = ["s3:ObjectCreated:*"]
  }
}