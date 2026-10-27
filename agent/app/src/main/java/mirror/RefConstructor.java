package mirror;

import java.lang.reflect.Constructor;
import java.lang.reflect.Field;

/* loaded from: classes2.dex */
public class RefConstructor<T> {
    private Constructor<?> ctor;

    public RefConstructor(Class<?> cls, Field field) throws NoSuchMethodException {
        if (field.isAnnotationPresent(MethodParams.class)) {
            Class<?>[] types = ((MethodParams) field.getAnnotation(MethodParams.class)).value();
            this.ctor = cls.getDeclaredConstructor(types);
        } else if (field.isAnnotationPresent(MethodReflectParams.class)) {
            String[] values = ((MethodReflectParams) field.getAnnotation(MethodReflectParams.class)).value();
            Class[] parameterTypes = new Class[values.length];
            int N = 0;
            while (N < values.length) {
                try {
                    Class<?> type = RefStaticMethod.getProtoType(values[N]);
                    parameterTypes[N] = type == null ? Class.forName(values[N]) : type;
                    N++;
                } catch (Exception e) {
                    e.printStackTrace();
                }
            }
            this.ctor = cls.getDeclaredConstructor(parameterTypes);
        } else {
            this.ctor = cls.getDeclaredConstructor(new Class[0]);
        }
        Constructor<?> constructor = this.ctor;
        if (constructor != null && !constructor.isAccessible()) {
            this.ctor.setAccessible(true);
        }
    }

    public T newInstance() {
        try {
            return (T) this.ctor.newInstance(new Object[0]);
        } catch (Exception e) {
            return null;
        }
    }

    public T newInstance(Object... objArr) {
        try {
            return (T) this.ctor.newInstance(objArr);
        } catch (Exception e) {
            return null;
        }
    }
}
