#!/bin/bash -ex

tmpdir=$(mktemp -d)

trap "rm -fr $tmpdir" EXIT

# good

mkdir $tmpdir/good
git -C $tmpdir/good init --quiet

cp good-action.yml $tmpdir/good/action.yml
mkdir -p $tmpdir/good/subaction
cp good-action.yml $tmpdir/good/subaction/action.yaml

mkdir -p $tmpdir/good/.forgejo/workflows
cp good-workflow.yml $tmpdir/good/.forgejo/workflows/action.yml
cp good-workflow.yml $tmpdir/good/.forgejo/workflows/workflow1.yml
cp good-workflow.yml $tmpdir/good/.forgejo/workflows/workflow2.yaml

# add workflows / actions that won't be good but it does not matter
# because they must be ignored
for i in .github .gitea; do
  mkdir -p $tmpdir/good/$i/workflows
  cp bad-workflow.yml $tmpdir/good/$i/workflows/bad.yml
done

git -C $tmpdir/good config user.email root@example.com
git -C $tmpdir/good config user.name username
git -C $tmpdir/good add .
git -C $tmpdir/good commit -m 'initial'

rm -fr good-repository
git clone --bare $tmpdir/good good-repository
rm -fr good-repository/hooks
touch good-repository/refs/placeholder

rm -fr good-directory
git clone $tmpdir/good good-directory
rm -fr good-directory/.git

# bad

mkdir $tmpdir/bad
git -C $tmpdir/bad init --quiet

cp bad-action.yml $tmpdir/bad/action.yml

mkdir -p $tmpdir/bad/.forgejo/workflows
cp bad-workflow.yml $tmpdir/bad/.forgejo/workflows/workflow1.yml

git -C $tmpdir/bad config user.email root@example.com
git -C $tmpdir/bad config user.name username
git -C $tmpdir/bad add .
git -C $tmpdir/bad commit -m 'initial'

rm -fr bad-repository
git clone --bare $tmpdir/bad bad-repository
rm -fr bad-repository/hooks
touch bad-repository/refs/placeholder

rm -fr bad-directory
git clone $tmpdir/bad bad-directory
rm -fr bad-directory/.git
