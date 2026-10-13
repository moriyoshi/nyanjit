import sys, re, collections
def load(p):
    d={}
    for l in open(p):
        parts=l.rstrip('\n').split(' ',7)
        d[' '.join(parts[:6])]=(parts[6],parts[7] if len(parts)>7 else '')
    return d
C=collections.Counter(); ex={}; tot=0
for g in sys.argv[2:]:
    pre=load(f'{sys.argv[1]}-{g}-pre.txt'); post=load(f'{sys.argv[1]}-{g}-post.txt')
    assert pre.keys()==post.keys()
    for k in pre:
        tot+=1
        (sa,ta),(sb,tb)=pre[k],post[k]; mut=k.split()[3]
        rule=re.sub(r'\d+','N',tb.split('predicate node N: ')[-1]) if 'predicate node' in tb else ''
        rule=re.sub(r'\d+','N',rule)
        if (sa,ta)==(sb,tb): c=('identical',sa,mut)
        elif sa=='OK' and sb=='OK': c=('LISTING',mut)
        elif sa=='ERR' and sb=='OK': c=('ERR->OK',mut)
        elif sa=='OK' and sb=='ERR': c=('OK->ERR',mut,rule or tb[:100])
        else: c=('ERRTEXT',mut,rule or tb[:100])
        C[c]+=1; ex.setdefault(c,(k,ta[:120],tb[:160]))
print('total',tot)
for c,n in sorted(C.items()): print(n,c,'eg:',ex[c])
