# 이 저장소가 인정하는 유일한 신원.
FD_EXPECT_IDENT="Junseong Park <kweizaa@gmail.com>"

# git var 는 "이름 <메일> 타임스탬프 시간대" 를 준다. 꼬리 둘을 떼고 비교한다.
_strip_stamp() { sed 's/ [0-9][0-9]* [-+][0-9][0-9]*$//'; }

check_ident() {
	_a=$(git var GIT_AUTHOR_IDENT | _strip_stamp)
	_c=$(git var GIT_COMMITTER_IDENT | _strip_stamp)
	_bad=0
	[ "$_a" = "$FD_EXPECT_IDENT" ] || { echo "거부: author 가 다르다 — $_a" >&2; _bad=1; }
	[ "$_c" = "$FD_EXPECT_IDENT" ] || { echo "거부: committer 가 다르다 — $_c" >&2; _bad=1; }
	if [ "$_bad" = 1 ]; then
		echo "  기대: $FD_EXPECT_IDENT" >&2
		echo "  고치려면: git config user.name 'Junseong Park' && git config user.email kweizaa@gmail.com" >&2
		echo "  (-c user.email=... 로 신원을 때우지 마라. 그게 x <x@x> 가 실려 나간 경로다)" >&2
	fi
	return $_bad
}
