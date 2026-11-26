package main

import (
	"os"

	"github.com/aws/aws-cdk-go/awscdk/v2"
	"github.com/aws/aws-cdk-go/awscdk/v2/awslambda"
	"github.com/aws/constructs-go/constructs/v10"
	"github.com/aws/jsii-runtime-go"
)

func newStack(scope constructs.Construct, id string, props *awscdk.StackProps) awscdk.Stack {
	stack := awscdk.NewStack(scope, &id, props)
	fn := awslambda.NewFunction(stack, jsii.String("Function"), &awslambda.FunctionProps{
		Runtime:      awslambda.Runtime_PROVIDED_AL2023(),
		Architecture: awslambda.Architecture_ARM_64(),
		Handler:      jsii.String("bootstrap"),
		Code:         awslambda.Code_FromAsset(jsii.String("build/lambda"), nil),
		MemorySize:   jsii.Number(256),
		Timeout:      awscdk.Duration_Seconds(jsii.Number(30)),
	})
	url := fn.AddFunctionUrl(&awslambda.FunctionUrlOptions{AuthType: awslambda.FunctionUrlAuthType_NONE})
	awscdk.NewCfnOutput(stack, jsii.String("ApiUrl"), &awscdk.CfnOutputProps{Value: url.Url()})
	return stack
}

func main() {
	defer jsii.Close()
	app := awscdk.NewApp(nil)

	region := os.Getenv("AWS_REGION")
	if region == "" {
		panic("AWS_REGION is not set")
	}
	account := os.Getenv("AWS_ACCOUNT_ID")
	if account == "" {
		panic("AWS_ACCOUNT_ID is not set")
	}

	props := &awscdk.StackProps{
		Env: &awscdk.Environment{
			Account: jsii.String(account),
			Region:  jsii.String(region),
		},
	}
	newStack(app, "BinaryLambdaStack", props)
	app.Synth(nil)
}
