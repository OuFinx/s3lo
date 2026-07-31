# vhs script for the README demo GIF
#
#   brew install vhs
#   mkdir -p docs/assets
#   AWS_PROFILE=<your-profile> AWS_REGION=us-east-1 vhs demo.tape
#
# Uses the installed s3lo binary and a throwaway s3://s3lo-demo/ bucket.
#
# To recreate that bucket from scratch, single platform only so the layers
# actually overlap, plus four tags of one app on a shared base so the dedup
# number and the layer-sharing matrix have something real to show:
#
#   aws s3api create-bucket --bucket s3lo-demo --region us-east-1
#   for i in alpine:3.20 node:22-alpine python:3.12-alpine nginx:1.27-alpine; do
#     s3lo copy $i s3://s3lo-demo/$i --platform linux/amd64
#   done
#
#   # Dockerfile: FROM node:22-alpine, one big identical layer standing in for
#   # node_modules, then COPY version.txt as the only thing that differs.
#   for v in 1.0.0 1.1.0 1.2.0 2.0.0; do
#     echo "myapp $v" > version.txt
#     docker build --platform linux/amd64 -t myapp:$v .
#     s3lo push myapp:$v s3://s3lo-demo/myapp:$v
#   done

Output docs/assets/demo.gif

Set Shell zsh
Set FontSize 15
Set Width 1200
Set Height 750
Set Padding 20
Set Theme "Catppuccin Mocha"
Set TypingSpeed 55ms

# Clean prompt and empty screen before anything is recorded.
Hide
Type "export PS1='$ '" Enter
Type "clear" Enter
Sleep 1s
Show

# What is in the bucket
Type "s3lo list s3://s3lo-demo/"
Sleep 400ms
Enter
Sleep 3s

Type "clear"
Enter
Sleep 300ms

# The number that sells the project
Type "s3lo stats s3://s3lo-demo/"
Sleep 400ms
Enter
Sleep 6s

Type "clear"
Enter
Sleep 300ms

# Layer sharing across tags
Type "s3lo stats s3://s3lo-demo/ --layers"
Sleep 400ms
Enter
Sleep 6s
