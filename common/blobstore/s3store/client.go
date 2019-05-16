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
	"bytes"
	"context"
	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/awserr"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/s3"
	"github.com/aws/aws-sdk-go/service/s3/s3iface"
	"github.com/uber/cadence/.gen/go/shared"
	"github.com/uber/cadence/common/backoff"
	"github.com/uber/cadence/common/blobstore"
	"github.com/uber/cadence/common/blobstore/blob"
	"go.uber.org/multierr"
	"io/ioutil"
	"math"
	"net/url"
)

var (
	// ErrCheckBucketExists could not verify that bucket directory exists
	ErrCheckBucketExists = &shared.BadRequestError{Message: "could not verify that bucket directory exists"}
	// ErrWriteFile could not write file
	ErrWriteFile = &shared.BadRequestError{Message: "could not write file"}
	// ErrReadFile could not read file
	ErrReadFile = &shared.BadRequestError{Message: "could not read file"}
	// ErrCheckFileExists could not check if file exists
	ErrCheckFileExists = &shared.BadRequestError{Message: "could not check if file exists"}
	// ErrDeleteFile could not delete file
	ErrDeleteFile = &shared.BadRequestError{Message: "could not delete file"}
	// ErrListFiles could not list files
	ErrListFiles = &shared.BadRequestError{Message: "could not list files"}
	// ErrConstructKey could not construct key
	ErrConstructKey = &shared.BadRequestError{Message: "could not construct key"}
	// ErrBucketConfigDeserialization bucket config could not be deserialized
	ErrBucketConfigDeserialization = &shared.BadRequestError{Message: "bucket config could not be deserialized"}
)

type client struct {
	s3cli s3iface.S3API
}

// NewClient returns a new Client backed by file system
func NewClient(cfg *Config) (blobstore.Client, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	sess := session.Must(session.NewSession(&aws.Config{
		Region: aws.String(cfg.Region),
	}))

	s3cli := s3.New(sess)
	return &client{
		s3cli: s3cli,
	}, nil
}
func s3upload(ctx context.Context, s3api s3iface.S3API, bucket string, key string, b []byte, tags url.Values) error {

	_, err := s3api.PutObjectWithContext(ctx, &s3.PutObjectInput{
		Bucket:  aws.String(bucket),
		Key:     aws.String(key),
		Body:    bytes.NewReader(b),
		Tagging: aws.String(tags.Encode()),
	})

	if err != nil {
		if aerr, ok := err.(awserr.Error); ok {

			if aerr.Code() == s3.ErrCodeNoSuchBucket {
				return blobstore.ErrBucketNotExists
			}

		}
		return err
	}
	return nil
}

func s3download(ctx context.Context, s3api s3iface.S3API, bucket string, key string) ([]byte, error) {

	result, err := s3api.GetObjectWithContext(ctx, &s3.GetObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(key),
	})

	if err != nil {
		if aerr, ok := err.(awserr.Error); ok {

			if aerr.Code() == s3.ErrCodeNoSuchBucket {
				return nil, blobstore.ErrBucketNotExists
			}

			if aerr.Code() == s3.ErrCodeNoSuchKey {
				return nil, ErrReadFile
			}

		}
		return nil, err

	}

	defer func() {
		ierr := result.Body.Close()
		if ierr != nil {
			err = multierr.Append(err, ierr)
		}
	}()

	body, err := ioutil.ReadAll(result.Body)

	return body, err
}

func s3gettags(ctx context.Context, s3api s3iface.S3API, bucket string, key string) (map[string]string, error) {

	result, err := s3api.GetObjectTaggingWithContext(ctx, &s3.GetObjectTaggingInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(key),
	})

	if err != nil {
		return nil, err
	}
	tags := make(map[string]string)
	for _, e := range result.TagSet {

		tags[*e.Key] = *e.Value

	}
	return tags, err
}
func (c *client) Upload(ctx context.Context, bucket string, key blob.Key, blob *blob.Blob) error {
	params := url.Values{}
	for k, v := range blob.Tags {
		params.Add(k, v)
	}

	return s3upload(ctx, c.s3cli, bucket, key.String(), blob.Body, params)
}

func (c *client) Download(ctx context.Context, bucket string, key blob.Key) (*blob.Blob, error) {

	b, err := s3download(ctx, c.s3cli, bucket, key.String())
	if err != nil {
		if err == ErrReadFile {
			return nil, blobstore.ErrBlobNotExists
		}
		return nil, err
	}
	tags, err := s3gettags(ctx, c.s3cli, bucket, key.String())
	if err != nil {
		return nil, err
	}
	return blob.NewBlob(b, tags), nil
}

func (c *client) GetTags(ctx context.Context, bucket string, key blob.Key) (map[string]string, error) {

	tags, err := s3gettags(ctx, c.s3cli, bucket, key.String())
	if err != nil {
		return nil, err
	}
	return tags, nil
}

func (c *client) Exists(ctx context.Context, bucket string, key blob.Key) (bool, error) {

	_, err := c.s3cli.HeadObjectWithContext(ctx, &s3.HeadObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(key.String()),
	})
	if err != nil {
		aerr, ok := err.(awserr.Error)
		if ok && aerr.Code() == s3.ErrCodeNoSuchKey {
			return false, nil
		}
		if ok && aerr.Code() == s3.ErrCodeNoSuchBucket {
			return false, blobstore.ErrBucketNotExists
		}

		return false, err
	}
	return true, nil
}

func (c *client) Delete(ctx context.Context, bucket string, key blob.Key) (bool, error) {

	_, err := c.s3cli.DeleteObjectWithContext(ctx, &s3.DeleteObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(key.String()),
	})
	if err != nil {
		if aerr, ok := err.(awserr.Error); ok {

			if aerr.Code() == s3.ErrCodeNoSuchBucket {
				return false, blobstore.ErrBucketNotExists
			}

			if aerr.Code() == s3.ErrCodeNoSuchKey {
				return false, nil
			}

		}
		return false, err

	}
	return true, nil
}

func (c *client) ListByPrefix(ctx context.Context, bucket string, prefix string) ([]blob.Key, error) {

	results, err := c.s3cli.ListObjectsV2WithContext(ctx, &s3.ListObjectsV2Input{
		Bucket: aws.String(bucket),
		Prefix: aws.String(prefix),
	})
	if err != nil {
		if aerr, ok := err.(awserr.Error); ok && aerr.Code() == s3.ErrCodeNoSuchBucket {
			return nil, blobstore.ErrBucketNotExists
		}
		return nil, ErrListFiles
	}
	var keys = make([]blob.Key, len(results.Contents))
	for i, v := range results.Contents {
		key, err := blob.NewKeyFromString(*v.Key)
		if err != nil {
			return nil, ErrConstructKey
		}
		keys[i] = key
	}
	return keys, nil
}

func (c *client) BucketMetadata(ctx context.Context, bucket string) (*blobstore.BucketMetadataResponse, error) {

	results, err := c.s3cli.GetBucketAclWithContext(ctx, &s3.GetBucketAclInput{
		Bucket: aws.String(bucket),
	})
	if err != nil {
		if aerr, ok := err.(awserr.Error); ok && aerr.Code() == s3.ErrCodeNoSuchBucket {
			return nil, blobstore.ErrBucketNotExists
		}
		return nil, err
	}

	lifecycleResults, err := c.s3cli.GetBucketLifecycleWithContext(ctx, &s3.GetBucketLifecycleInput{
		Bucket: aws.String(bucket),
	})
	if err != nil {
		if aerr, ok := err.(awserr.Error); ok && aerr.Code() == s3.ErrCodeNoSuchBucket {
			return nil, blobstore.ErrBucketNotExists
		}
		return nil, err
	}
	var retentionDays = math.MaxInt64
	for _, v := range lifecycleResults.Rules {
		if retentionDays > int(*v.Expiration.Days) {
			retentionDays = int(*v.Expiration.Days)
		}
	}
	return &blobstore.BucketMetadataResponse{
		Owner:         *results.Owner.DisplayName,
		RetentionDays: retentionDays,
	}, nil
}

func (c *client) BucketExists(ctx context.Context, bucket string) (bool, error) {

	_, err := c.s3cli.HeadBucketWithContext(ctx, &s3.HeadBucketInput{
		Bucket: aws.String(bucket),
	})

	if err != nil {
		if aerr, ok := err.(awserr.Error); ok && aerr.Code() == s3.ErrCodeNoSuchBucket {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

func (c *client) IsRetryableError(err error) bool {
	return false
}

func (c *client) GetRetryPolicy() backoff.RetryPolicy {
	policy := backoff.NewExponentialRetryPolicy(0)
	policy.SetMaximumAttempts(1)
	return policy
}
