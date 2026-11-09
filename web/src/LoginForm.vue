<script setup lang="ts">
import {ref} from 'vue'
import {Boxes,Eye,EyeOff,LogIn} from '@lucide/vue'
defineProps<{busy:boolean;error:string}>()
const emit=defineEmits<{submit:[username:string,password:string]}>()
const username=ref(''),password=ref(''),visible=ref(false)
function submit(){emit('submit',username.value.trim(),password.value);password.value=''}
</script>
<template>
<div class="login"><form class="login-form" @submit.prevent="submit">
<div class="brand"><Boxes :size="26"/> OLIXOPS <span>AGENT PLATFORM</span></div>
<div class="eyebrow">LOCAL ACCOUNT</div><h1>登录 AI 沙箱工作台</h1><p>使用本地账号访问你的项目与开发环境。</p>
<label for="username">账号<input id="username" v-model="username" name="username" autocomplete="username" placeholder="请输入账号" maxlength="64" required :disabled="busy" autofocus/></label>
<label for="password">密码<div class="password-input"><input id="password" v-model="password" name="password" autocomplete="current-password" :type="visible?'text':'password'" placeholder="请输入密码" maxlength="72" required :disabled="busy"/><button type="button" class="icon" :aria-label="visible?'隐藏密码':'显示密码'" :aria-pressed="visible" @click="visible=!visible"><component :is="visible?EyeOff:Eye" :size="16"/></button></div></label>
<div v-if="error" role="alert" class="error">{{error}}</div>
<button class="primary login-submit" :disabled="busy"><LogIn :size="16"/>{{busy?'登录中…':'登录'}}</button>
<small>账号由管理员创建。公司统一登录将在后续接入。</small>
</form></div>
</template>
<style scoped>
.password-input{display:flex;align-items:center;position:relative}.password-input input{width:100%;padding-right:48px}.password-input .icon{position:absolute;right:7px;border:0;background:transparent}.login-submit{margin-top:24px}.login-form small{margin-bottom:0}.login-form label{margin-top:20px}
</style>
