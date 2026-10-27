package mirror;

import java.lang.reflect.Field;
import java.lang.reflect.InvocationTargetException;
import java.lang.reflect.Method;

/* loaded from: classes2.dex */
public class RefStaticMethod<T> {
    private Method method;
    private String name;
    private String parent;

    public RefStaticMethod(Class<?> cls, Field field) throws NoSuchMethodException, SecurityException, ClassNotFoundException {
        this.name = field.getName();
        this.parent = cls.getName();
        if (field.isAnnotationPresent(MethodParams.class)) {
            Class<?>[] types = ((MethodParams) field.getAnnotation(MethodParams.class)).value();
            for (int i = 0; i < types.length; i++) {
                Class<?> clazz = types[i];
                if (clazz.getClassLoader() == getClass().getClassLoader()) {
                    try {
                        Class.forName(clazz.getName());
                        Class<?> realClass = (Class) clazz.getField("TYPE").get(null);
                        types[i] = realClass;
                    } catch (Throwable e) {
                        throw new RuntimeException(e);
                    }
                }
            }
            Method declaredMethod = cls.getDeclaredMethod(field.getName(), types);
            this.method = declaredMethod;
            declaredMethod.setAccessible(true);
        } else if (field.isAnnotationPresent(MethodReflectParams.class)) {
            boolean arrayset = false;
            String[] typeNames = ((MethodReflectParams) field.getAnnotation(MethodReflectParams.class)).value();
            Class<?>[] types2 = new Class[typeNames.length];
            Class<?>[] types22 = new Class[typeNames.length];
            for (int i2 = 0; i2 < typeNames.length; i2++) {
                Class<?> type = getProtoType(typeNames[i2]);
                if (type == null) {
                    try {
                        type = Class.forName(typeNames[i2]);
                    } catch (ClassNotFoundException e2) {
                        e2.printStackTrace();
                    }
                }
                types2[i2] = type;
                if ("java.util.HashSet".equals(typeNames[i2])) {
                    arrayset = true;
                    Class<?> type2 = type;
                    try {
                        type2 = Class.forName("android.util.ArraySet");
                    } catch (ClassNotFoundException e3) {
                    }
                    if (type2 != null) {
                        types22[i2] = type2;
                    } else {
                        types22[i2] = type;
                    }
                } else {
                    types22[i2] = type;
                }
            }
            try {
                this.method = cls.getDeclaredMethod(field.getName(), types2);
            } catch (Exception e4) {
                e4.printStackTrace();
                if (arrayset) {
                    this.method = cls.getDeclaredMethod(field.getName(), types22);
                }
            }
            this.method.setAccessible(true);
        } else {
            Method[] declaredMethods = cls.getDeclaredMethods();
            int length = declaredMethods.length;
            int i3 = 0;
            while (true) {
                if (i3 >= length) {
                    break;
                }
                Method method = declaredMethods[i3];
                if (!method.getName().equals(field.getName())) {
                    i3++;
                } else {
                    this.method = method;
                    method.setAccessible(true);
                    break;
                }
            }
        }
        if (this.method == null) {
            throw new NoSuchMethodException(field.getName());
        }
    }

    static Class<?> getProtoType(String typeName) {
        if (typeName.equals("int")) {
            return Integer.TYPE;
        }
        if (typeName.equals("long")) {
            return Long.TYPE;
        }
        if (typeName.equals("boolean")) {
            return Boolean.TYPE;
        }
        if (typeName.equals("byte")) {
            return Byte.TYPE;
        }
        if (typeName.equals("short")) {
            return Short.TYPE;
        }
        if (typeName.equals("char")) {
            return Character.TYPE;
        }
        if (typeName.equals("float")) {
            return Float.TYPE;
        }
        if (typeName.equals("double")) {
            return Double.TYPE;
        }
        if (typeName.equals("void")) {
            return Void.TYPE;
        }
        return null;
    }

    public T call(Object... objArr) {
        try {
            return (T) this.method.invoke(null, objArr);
        } catch (Exception e) {
            e.printStackTrace();
            return null;
        }
    }

    public T callWithException(Object... objArr) throws Throwable {
        try {
            return (T) this.method.invoke(null, objArr);
        } catch (InvocationTargetException e) {
            if (e.getCause() != null) {
                throw e.getCause();
            }
            throw e;
        }
    }

    public String toString() {
        StringBuilder sb = new StringBuilder();
        sb.append("RefStaticMethod{");
        sb.append(this.parent);
        sb.append("@");
        sb.append(this.name);
        sb.append(" find=");
        sb.append(this.method != null);
        sb.append('}');
        return sb.toString();
    }
}
