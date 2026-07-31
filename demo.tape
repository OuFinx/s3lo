# vhs script for the README demo GIF
#
#   brew install vhs
#   go build -o s3lo ./cmd/s3lo          # the tape puts $PWD first on PATH,
#   mkdir -p docs/assets                 # so it records THIS build, not
#   AWS_PROFILE=<profile> AWS_REGION=us-east-1 vhs demo.tape
#
# Uses a throwaway s3://s3lo-demo/ bucket. To recreate it, single platform only
# so the layers actually overlap, plus four tags of one app whose only
# difference lives *inside* one big layer — that is what makes the chunk count
# in the push line mean something:
#
#   aws s3api create-bucket --bucket s3lo-demo --region us-east-1
#   for i in alpine:3.20 node:22-alpine python:3.12-alpine nginx:1.27-alpine; do
#     s3lo copy $i s3://s3lo-demo/$i --platform linux/amd64
#   done
#
#   # Dockerfile: FROM node:22-alpine, one RUN that builds a ~50 MB
#   # node_modules stand-in from files already in the image (deterministic, so
#   # it is byte-identical between builds) and writes $VERSION into it.
#   for v in 1.0.0 1.1.0 1.2.0 2.0.0; do
#     docker build --platform linux/amd64 --build-arg VERSION=$v -t myapp:$v .
#     s3lo push myapp:$v s3://s3lo-demo/myapp:$v
#   done
#   # Build one more tag but do NOT push it: the recording pushes it live,
#   # which is where the chunk count in the output comes from. If you re-record,
#   # bump that version — a tag already in the bucket dedups to 100% and the
#   # chunk count goes to 0/54, which shows nothing.

Output docs/assets/demo.gif

Set Shell zsh
Set FontSize 15
Set Width 1200
Set Height 440
Set Padding 20
Set Theme "Catppuccin Mocha"
Set TypingSpeed 95ms

# Clean prompt, and record the binary built from this checkout rather than
# whatever version happens to be installed.
Hide
Type "export PATH=$PWD:$PATH" Enter
Type "export PS1='$ '" Enter
Type "clear" Enter
Sleep 1s
Show

# What is in the bucket.
Type "s3lo list s3://s3lo-demo/"
Sleep 800ms
Enter
Sleep 3s

Type "clear"
Enter
Sleep 400ms

# The point of the whole project: one file changed inside a big layer, so only
# the chunks holding it go up. A registry re-uploads the entire layer.
Type "s3lo push myapp:3.0.0 s3://s3lo-demo/myapp:3.0.0"
Sleep 800ms
Enter
Sleep 11s

Type "clear"
Enter
Sleep 400ms

# Read one file out of an image without pulling it. A registry cannot: the
# distribution spec addresses whole layers.
Type "s3lo cat s3://s3lo-demo/myapp:2.0.0 /app/node_modules/version.txt"
Sleep 800ms
Enter
Sleep 4s

Type "clear"
Enter
Sleep 400ms

# Which layers are shared, by what, and what that saves. The plain `stats`
# view is left out on purpose: on a bucket this small every cost line reads
# "< $0.01/month", which undercuts the point it is there to make.
Type "s3lo stats s3://s3lo-demo/myapp --layers"
Sleep 800ms
Enter
Sleep 7s
