/*
* Copyright IBM Corp. 2018 All Rights Reserved.
*
* Licensed under the Apache License, Version 2.0 (the "License");
* you may not use this file except in compliance with the License.
* You may obtain a copy of the License at
*
*     http://www.apache.org/licenses/LICENSE-2.0
*
* Unless required by applicable law or agreed to in writing, software
* distributed under the License is distributed on an "AS IS" BASIS,
* WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
* See the License for the specific language governing permissions and
* limitations under the License.
 */

package main

import (
	"fmt"
	"reflect"

	commonerrors "github.com/hyperledger/fabric/common/errors"
	"github.com/hyperledger/fabric/core/handlers/validation/api"
	. "github.com/hyperledger/fabric/core/handlers/validation/api/capabilities"
	. "github.com/hyperledger/fabric/core/handlers/validation/api/identities"
	. "github.com/hyperledger/fabric/core/handlers/validation/api/policies"
	. "github.com/hyperledger/fabric/core/handlers/validation/api/state"
	default_vscc "github.com/hyperledger/fabric/core/handlers/validation/builtin"
	"github.com/hyperledger/fabric/protos/common"
	"github.com/pkg/errors"
)

func NewPluginFactory() validation.PluginFactory {
	return &ECCValidationFactory{}
}

type ECCValidationFactory struct {
}

func (*ECCValidationFactory) New() validation.Plugin {
	return &ECCValidation{}
}

type ECCValidation struct {
	DefaultTxValidator TransactionValidator
	ECCTxValidator     TransactionValidator
}

//go:generate mockery -dir . -name TransactionValidator -case underscore -output mocks/
type TransactionValidator interface {
	Validate(txData []byte, policy []byte) commonerrors.TxValidationError
}

func (v *ECCValidation) Validate(block *common.Block, namespace string, txPosition int, actionPosition int, contextData ...validation.ContextDatum) error {
	if len(contextData) == 0 {
		logger.Panicf("Expected to receive policy bytes in context data")
	}

	serializedPolicy, isSerializedPolicy := contextData[0].(SerializedPolicy)
	if !isSerializedPolicy {
		logger.Panicf("Expected to receive a serialized policy in the first context data")
	}
	if block == nil || block.Data == nil {
		return errors.New("empty block")
	}
	if txPosition >= len(block.Data.Data) {
		return errors.Errorf("block has only %d transactions, but requested tx at position %d", len(block.Data.Data), txPosition)
	}
	if block.Header == nil {
		return errors.Errorf("no block header")
	}

	// do defalt vscc
	err := v.DefaultTxValidator.Validate(block.Data.Data[txPosition], serializedPolicy.Bytes())
	if err != nil {
		logger.Debugf("block %d, namespace: %s, tx %d validation results is: %v", block.Header.Number, namespace, txPosition, err)
		return convertErrorTypeOrPanic(err)
	}

	// do ecc-vscc
	err = v.ECCTxValidator.Validate(block.Data.Data[txPosition], serializedPolicy.Bytes())
	logger.Debugf("block %d, namespace: %s, tx %d validation results is: %v", block.Header.Number, namespace, txPosition, err)
	return convertErrorTypeOrPanic(err)

}

func convertErrorTypeOrPanic(err error) error {
	if err == nil {
		return nil
	}
	if err, isExecutionError := err.(*commonerrors.VSCCExecutionFailureError); isExecutionError {
		return &validation.ExecutionFailureError{
			Reason: err.Error(),
		}
	}
	if err, isEndorsementError := err.(*commonerrors.VSCCEndorsementPolicyError); isEndorsementError {
		return err
	}
	logger.Panicf("Programming error: The error is %v, of type %v but expected to be either ExecutionFailureError or VSCCEndorsementPolicyError", err, reflect.TypeOf(err))
	return &validation.ExecutionFailureError{Reason: fmt.Sprintf("error of type %v returned from VSCC", reflect.TypeOf(err))}
}

func (v *ECCValidation) Init(dependencies ...validation.Dependency) error {
	var (
		d  IdentityDeserializer
		c  Capabilities
		sf StateFetcher
		pe PolicyEvaluator
	)
	for _, dep := range dependencies {
		if deserializer, isIdentityDeserializer := dep.(IdentityDeserializer); isIdentityDeserializer {
			d = deserializer
		}
		if capabilities, isCapabilities := dep.(Capabilities); isCapabilities {
			c = capabilities
		}
		if stateFetcher, isStateFetcher := dep.(StateFetcher); isStateFetcher {
			sf = stateFetcher
		}
		if policyEvaluator, isPolicyFetcher := dep.(PolicyEvaluator); isPolicyFetcher {
			pe = policyEvaluator
		}
	}
	if sf == nil {
		return errors.New("stateFetcher not passed in init")
	}
	if d == nil {
		return errors.New("identityDeserializer not passed in init")
	}
	if c == nil {
		return errors.New("capabilities not passed in init")
	}
	if pe == nil {
		return errors.New("policy fetcher not passed in init")
	}
	// use default vscc and our custom ercc vscc
	v.DefaultTxValidator = default_vscc.New(c, sf, d, pe)
	v.ECCTxValidator = New(sf)

	return nil
}
