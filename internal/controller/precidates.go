package controller

import (
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
)

var predicatePVC = predicate.NewPredicateFuncs(func(object client.Object) bool {
	return false
})

var predicatePod = predicate.NewPredicateFuncs(func(object client.Object) bool {
	return false
})
