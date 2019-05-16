// Copyright (c) 2017 Uber Technologies, Inc.
//
// Permission is hereby granted, free of charge, to any person obtaining a copy
// of this software and associated documentation files (the "Software"), to deal
// in the Software without restriction, including without limitation the rights
// to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
// copies of the Software, and to permit persons to whom the Software is
// furnished to do so, subject to the following conditions:
//
// The above copyright notice and this permission notice shall be included in
// all copies or substantial portions of the Software.
//
// THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
// IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
// FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
// AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
// LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
// OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN
// THE SOFTWARE.

package s3store

import (
	"errors"
)

type (
	// Config describes the configuration needed to construct a blobstore client backed by file system
	Config struct {
		Region        string         `yaml:"region"`
		DefaultBucket BucketConfig   `yaml:"defaultBucket"`
		CustomBuckets []BucketConfig `yaml:"customBuckets"`
	}

	// BucketConfig describes the config for a bucket
	BucketConfig struct {
		Name string `yaml:"name"`
	}
)

// Validate validates config
func (c *Config) Validate() error {
	validateBucketConfig := func(b BucketConfig) error {
		// s3 buckets must be between 3 and 63 charachters long
		if len(b.Name) < 3 {
			return errors.New("empty or invalid bucket name")
		}
		return nil
	}

	if len(c.Region) == 0 {
		return errors.New("empty aws region")
	}
	if err := validateBucketConfig(c.DefaultBucket); err != nil {
		return err
	}
	for _, b := range c.CustomBuckets {
		if err := validateBucketConfig(b); err != nil {
			return err
		}
	}
	return nil
}
